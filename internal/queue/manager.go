package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"rytm/internal/ipc"
	"rytm/internal/resolve"
)

type Manager struct {
	mu       sync.RWMutex
	tasks    map[string]*Task
	trackSem chan struct{}
	resolver *resolve.Resolver // nil = skip resolution (graceful degradation)
}

func NewManager(resolver *resolve.Resolver) *Manager {
	return &Manager{
		tasks:    make(map[string]*Task),
		trackSem: make(chan struct{}, 3),
		resolver: resolver,
	}
}
func isPlaylistURL(query string) bool {
	if !strings.HasPrefix(query, "http://") && !strings.HasPrefix(query, "https://") {
		return false
	}
	u, err := url.Parse(query)
	if err != nil {
		return false
	}
	if strings.Contains(u.Path, "/playlist") && u.Query().Get("list") != "" {
		return true
	}
	if strings.Contains(u.Path, "/album/") {
		return true
	}
	return false
}

func (m *Manager) Submit(query string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	taskID := fmt.Sprintf("task_%d", len(m.tasks)+1)
	ctx, cancel := context.WithCancel(context.Background())
	task := &Task{
		ID:         taskID,
		SessionID:  taskID,
		Query:      query,
		Title:      "",
		Artist:     "",
		Status:     "Pending",
		IsPlaylist: isPlaylistURL(query),
		CancelFn:   cancel,
	}
	m.tasks[taskID] = task

	if task.IsPlaylist {
		go m.runPlaylistTask(ctx, task)
	} else {
		go m.runTask(ctx, task)
	}
	return taskID
}

// runTask is the two-phase pipeline:
//
//  1. downloadRaw  — yt-dlp writes the raw audio stream to a temp file.
//  2. FetchMetadata — fpcalc → AcoustID → MusicBrainz → Cover Art Archive.
//  3. transcodeWithMeta — FFmpeg converts to AAC/m4a and injects all tags in
//     the same pass where the volume filter is applied.
func (m *Manager) runTask(ctx context.Context, t *Task) {
	// ── Phase 0: Pre-fetch resolution ──────────────────────────────────
	if m.resolver != nil && !isDirectURL(t.Query) {
		t.SetStatus("Resolving", "")
		result, err := m.resolver.Resolve(ctx, t.Query)
		if err == nil {
			t.mu.Lock()
			t.ResolvedURL = result.URL
			t.mu.Unlock()
			if result.Entity.Title != "" {
				t.SetTitle(result.Entity.Title)
			}
			if len(result.Entity.Artists) > 0 {
				t.SetArtist(result.Entity.Artists[0])
			}
			// If the result is a multi-track entity (album/playlist), expand tracks
			if result.IsMulti && len(result.Tracks) > 0 {
				t.SetStatus("Done", "")
				albumTitle := sanitizeFilename(result.Entity.Title)
				if albumTitle == "" {
					albumTitle = "Playlist"
				}
				for i, track := range result.Tracks {
					m.SubmitTrack(track.URL(), albumTitle, i+1, t.SessionID)
				}
				return
			}
		}
		// On resolution failure: log and fall through to yt-dlp ytsearch
	}

	t.SetStatus("Queued", "")
	select {
	case m.trackSem <- struct{}{}:
		defer func() { <-m.trackSem }()
	case <-ctx.Done():
		t.SetStatus("Cancelled", "Cancelled while queued")
		return
	}

	t.SetStatus("Downloading", "")
	// ── Phase 1: Raw audio download ──────────────────────────────────────────
	rawPath, ytMeta, videoTitle, err := m.downloadRaw(ctx, t)
	if err != nil {
		select {
		case <-ctx.Done():
			t.SetStatus("Cancelled", "Download cancelled by user")
		default:
			t.SetStatus("Failed", fmt.Sprintf("download: %v", err))
		}
		return
	}
	defer os.Remove(rawPath)
	// Clean up any downloaded thumbnail at the end of the task
	thumbnailPath := findDownloadedThumbnail(t.ID)
	if thumbnailPath != "" {
		defer os.Remove(thumbnailPath)
	}
	// ── Phase 2: Acoustic fingerprinting ────────────────────────────────────
	t.SetStatus("Fingerprinting", "")
	meta, fpErr := FetchMetadata(ctx, rawPath)
	if fpErr != nil {
		select {
		case <-ctx.Done():
			t.SetStatus("Cancelled", "Fingerprinting cancelled by user")
			return
		default:
			// Non-fatal: degrade gracefully — fall back to video title parsing.
			meta = ytMeta
		}
	}
	if t.PlaylistTrackNum > 0 {
		meta.TrackNum = strconv.Itoa(t.PlaylistTrackNum)
	}
	if meta.Title != "" {
		t.SetTitle(meta.Title)
	}
	if meta.Artist != "" {
		t.SetArtist(meta.Artist)
	}
	if meta.Album != "" {
		t.SetAlbum(meta.Album)
	}
	// Write cover art bytes to a temp file so FFmpeg can open it as a second
	// input stream. The file is cleaned up regardless of transcode outcome.
	coverPath := ""
	if len(meta.CoverData) > 0 {
		coverPath = filepath.Join(os.TempDir(), fmt.Sprintf("rytm_%s_cover.jpg", t.ID))
		if writeErr := os.WriteFile(coverPath, meta.CoverData, 0644); writeErr != nil {
			coverPath = "" // non-fatal; continue without art
		} else {
			defer os.Remove(coverPath)
		}
	}
	if coverPath == "" && thumbnailPath != "" {
		coverPath = thumbnailPath
	}
	// ── Phase 3: Single-pass FFmpeg transcode ────────────────────────────────
	t.SetStatus("Tagging", "")
	outName := buildOutputName(meta, videoTitle, t.Query)

	if t.OutputDir != "" {
		if err := os.MkdirAll(t.OutputDir, 0755); err == nil {
			outName = filepath.Join(t.OutputDir, outName)
		}
	}
	if err := m.transcodeWithMeta(ctx, rawPath, coverPath, meta, outName, t.ID); err != nil {
		select {
		case <-ctx.Done():
			t.SetStatus("Cancelled", "Transcode cancelled by user")
		default:
			t.SetStatus("Failed", fmt.Sprintf("transcode: %v", err))
		}
		return
	}
	t.SetStatus("Done", "")
}
func (m *Manager) Cancel(taskID string) bool {
	m.mu.RLock()
	task, exists := m.tasks[taskID]
	m.mu.RUnlock()
	if !exists {
		return false
	}
	task.mu.Lock()
	defer task.mu.Unlock()
	if task.Status == "Done" || task.Status == "Failed" || task.Status == "Cancelled" {
		return false
	}
	task.Status = "Cancelled"
	if task.CancelFn != nil {
		task.CancelFn()
	}
	if task.Cmd != nil && task.Cmd.Process != nil {
		_ = task.Cmd.Process.Kill()
	}
	return true
}
func (m *Manager) GetTask(taskID string) (ipc.TaskStatus, bool) {
	m.mu.RLock()
	task, exists := m.tasks[taskID]
	m.mu.RUnlock()
	if !exists {
		return ipc.TaskStatus{}, false
	}
	return task.GetStatus(), true
}
func (m *Manager) GetAllTasks() []ipc.TaskStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	statuses := make([]ipc.TaskStatus, 0, len(m.tasks))
	for _, task := range m.tasks {
		statuses = append(statuses, task.GetStatus())
	}
	return statuses
}
func (m *Manager) runPlaylistTask(ctx context.Context, t *Task) {
	t.SetStatus("Fetching Playlist", "")

	cmd := exec.CommandContext(ctx, "yt-dlp", "-J", "--flat-playlist", t.Query)
	out, err := cmd.Output()
	if err != nil {
		t.SetStatus("Failed", fmt.Sprintf("fetch playlist: %v", err))
		return
	}

	var playlistData struct {
		Title   string `json:"title"`
		Entries []struct {
			ID    string `json:"id"`
			URL   string `json:"url"`
			Title string `json:"title"`
		} `json:"entries"`
	}

	if err := json.Unmarshal(out, &playlistData); err != nil {
		t.SetStatus("Failed", fmt.Sprintf("parse playlist json: %v", err))
		return
	}

	playlistTitle := sanitizeFilename(playlistData.Title)
	if playlistTitle == "" {
		playlistTitle = "Playlist"
	}

	t.SetTitle(playlistTitle)
	t.SetStatus("Done", "")

	for i, entry := range playlistData.Entries {
		entryURL := entry.URL
		if entryURL == "" && entry.ID != "" {
			entryURL = "https://www.youtube.com/watch?v=" + entry.ID
		}
		if entryURL != "" {
			m.SubmitTrack(entryURL, playlistTitle, i+1, t.SessionID)
		}
	}
}

func (m *Manager) SubmitTrack(query, outputDir string, trackNum int, sessionID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	taskID := fmt.Sprintf("task_%d", len(m.tasks)+1)
	ctx, cancel := context.WithCancel(context.Background())
	task := &Task{
		ID:               taskID,
		SessionID:        sessionID,
		Query:            query,
		Title:            "",
		Artist:           "",
		Status:           "Pending",
		OutputDir:        outputDir,
		IsPlaylist:       true,
		PlaylistName:     outputDir,
		PlaylistTrackNum: trackNum,
		CancelFn:         cancel,
	}
	m.tasks[taskID] = task
	go m.runTask(ctx, task)
	return taskID
}

func (m *Manager) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, task := range m.tasks {
		task.mu.Lock()
		if task.Status != "Done" && task.Status != "Failed" && task.Status != "Cancelled" {
			task.Status = "Cancelled"
			if task.CancelFn != nil {
				task.CancelFn()
			}
			if task.Cmd != nil && task.Cmd.Process != nil {
				_ = task.Cmd.Process.Kill()
			}
		}
		task.mu.Unlock()
	}
}

// isDirectURL returns true if the query is already a YouTube/URL — no resolution needed.
func isDirectURL(query string) bool {
	return strings.HasPrefix(query, "http://") || strings.HasPrefix(query, "https://")
}

