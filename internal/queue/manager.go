package queue

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"rytm/internal/ipc"
	"sync"
)
type Manager struct {
	mu    sync.RWMutex
	tasks map[string]*Task
}
func NewManager() *Manager {
	return &Manager{
		tasks: make(map[string]*Task),
	}
}
func (m *Manager) Submit(query string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	taskID := fmt.Sprintf("task_%d", len(m.tasks)+1)
	ctx, cancel := context.WithCancel(context.Background())
	task := &Task{
		ID:       taskID,
		Query:    query,
		Title:    "",
		Artist:   "",
		Status:   "Pending",
		Progress: 0,
		CancelFn: cancel,
	}
	m.tasks[taskID] = task
	go m.runTask(ctx, task)
	return taskID
}
// runTask is the two-phase pipeline:
//
//  1. downloadRaw  — yt-dlp writes the raw audio stream to a temp file.
//  2. FetchMetadata — fpcalc → AcoustID → MusicBrainz → Cover Art Archive.
//  3. transcodeWithMeta — FFmpeg converts to AAC/m4a and injects all tags in
//     the same pass where the volume filter is applied.
func (m *Manager) runTask(ctx context.Context, t *Task) {
	t.SetStatus("Downloading", 0, "")
	// ── Phase 1: Raw audio download ──────────────────────────────────────────
	rawPath, ytMeta, videoTitle, err := m.downloadRaw(ctx, t)
	if err != nil {
		select {
		case <-ctx.Done():
			t.SetStatus("Cancelled", t.Progress, "Download cancelled by user")
		default:
			t.SetStatus("Failed", t.Progress, fmt.Sprintf("download: %v", err))
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
	t.SetStatus("Fingerprinting", 99, "")
	meta, fpErr := FetchMetadata(ctx, rawPath)
	if fpErr != nil {
		select {
		case <-ctx.Done():
			t.SetStatus("Cancelled", 99, "Fingerprinting cancelled by user")
			return
		default:
			// Non-fatal: degrade gracefully — fall back to video title parsing.
			meta = ytMeta
		}
	}
	if meta.Title != "" {
		t.SetTitle(meta.Title)
	}
	if meta.Artist != "" {
		t.SetArtist(meta.Artist)
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
	t.SetStatus("Tagging", 99, "")
	outName := buildOutputName(meta, videoTitle, t.Query)
	if err := m.transcodeWithMeta(ctx, rawPath, coverPath, meta, outName, t.ID); err != nil {
		select {
		case <-ctx.Done():
			t.SetStatus("Cancelled", 99, "Transcode cancelled by user")
		default:
			t.SetStatus("Failed", 99, fmt.Sprintf("transcode: %v", err))
		}
		return
	}
	t.SetStatus("Done", 100, "")
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