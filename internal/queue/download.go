package queue

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"rytm/internal/ipc"
)

type Task struct {
	ID       string
	Query    string
	Title    string
	Artist   string
	Status   string // "Pending", "Downloading", "Fingerprinting", "Tagging", "Done", "Failed", "Cancelled"
	Progress int    // 0 to 100
	Error    string
	Cmd      *exec.Cmd
	CancelFn context.CancelFunc
	mu       sync.RWMutex
}

func (t *Task) GetStatus() ipc.TaskStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return ipc.TaskStatus{
		TaskID:   t.ID,
		Query:    t.Query,
		Title:    t.Title,
		Artist:   t.Artist,
		Status:   t.Status,
		Progress: t.Progress,
		Error:    t.Error,
	}
}

func (t *Task) SetStatus(status string, progress int, errMsg string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Status = status
	t.Progress = progress
	t.Error = errMsg
}

func (t *Task) SetTitle(title string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Title = title
}

func (t *Task) SetArtist(artist string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Artist = artist
}

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

// downloadRaw runs yt-dlp without any post-processing flags so that the raw
// audio stream is written to a deterministic temp file. The actual file
// extension is chosen by yt-dlp (usually .webm / .opus); we resolve it with a
// glob after the process exits.
func (m *Manager) downloadRaw(ctx context.Context, t *Task) (string, TrackMeta, string, error) {
	queryArg := t.Query
	if !strings.HasPrefix(t.Query, "http://") && !strings.HasPrefix(t.Query, "https://") {
		queryArg = fmt.Sprintf(`ytsearch1:%s "Provided to YouTube"`, strings.TrimSpace(t.Query))
	}

	// Use the task ID in the filename to guarantee uniqueness across concurrent
	// tasks and to make the glob below unambiguous.
	rawTemplate := filepath.Join(os.TempDir(), fmt.Sprintf("rytm_%s.%%(ext)s", t.ID))

	args := []string{
		"--cookies", "cookies.txt",
		queryArg,
		"-f", "bestaudio[ext=webm]/bestaudio/bestaudio",
		"--no-embed-metadata",
		"--no-embed-thumbnail",
		"-o", rawTemplate,
		"--write-thumbnail",
		"--convert-thumbnails", "jpg",
		"--encoding", "utf-8",
		"--print", "rytm_title:%(title)s",
		"--print", "rytm_artist:%(artist)s",
		"--print", "rytm_album:%(album)s",
		"--print", "rytm_track:%(track)s",
		"--print", "rytm_release_year:%(release_year)s",
		"--no-simulate",
	}

	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
	cmd.Dir = "./"
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8", "PYTHONUTF8=1")

	t.mu.Lock()
	t.Cmd = cmd
	t.mu.Unlock()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", TrackMeta{}, "", fmt.Errorf("stdout pipe: %v", err)
	}
	cmd.Stderr = cmd.Stdout // merge stderr into the same pipe

	if err := cmd.Start(); err != nil {
		return "", TrackMeta{}, "", fmt.Errorf("yt-dlp start: %v", err)
	}

	reProgress := regexp.MustCompile(`\[download\]\s+([0-9.]+)%`)
	rePostProc := regexp.MustCompile(`\[(ExtractAudio|Metadata|Thumbnails|FixupM4a)\]`)
	reTitle := regexp.MustCompile(`^rytm_title:(.*)$`)
	reArtist := regexp.MustCompile(`^rytm_artist:(.*)$`)
	reAlbum := regexp.MustCompile(`^rytm_album:(.*)$`)
	reTrack := regexp.MustCompile(`^rytm_track:(.*)$`)
	reYear := regexp.MustCompile(`^rytm_release_year:(.*)$`)

	var videoTitle, ytArtist, ytAlbum, ytTrack, ytYear string
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case reTitle.MatchString(line):
			if m := reTitle.FindStringSubmatch(line); len(m) > 1 {
				videoTitle = strings.TrimSpace(m[1])
				t.SetTitle(videoTitle)
			}
		case reArtist.MatchString(line):
			if m := reArtist.FindStringSubmatch(line); len(m) > 1 {
				ytArtist = strings.TrimSpace(m[1])
				t.SetArtist(ytArtist)
			}
		case reAlbum.MatchString(line):
			if m := reAlbum.FindStringSubmatch(line); len(m) > 1 {
				ytAlbum = strings.TrimSpace(m[1])
			}
		case reTrack.MatchString(line):
			if m := reTrack.FindStringSubmatch(line); len(m) > 1 {
				ytTrack = strings.TrimSpace(m[1])
				t.SetTitle(ytTrack)
			}
		case reYear.MatchString(line):
			if m := reYear.FindStringSubmatch(line); len(m) > 1 {
				ytYear = strings.TrimSpace(m[1])
			}
		case reProgress.MatchString(line):
			if m := reProgress.FindStringSubmatch(line); len(m) > 1 {
				if pct, err := strconv.ParseFloat(m[1], 64); err == nil {
					progress := int(pct)
					if progress >= 100 {
						progress = 99
					}
					t.SetStatus("Downloading", progress, "")
				}
			}
		case rePostProc.MatchString(line):
			t.SetStatus("Downloading", 99, "")
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		_ = cmd.Wait()
		return "", TrackMeta{}, "", fmt.Errorf("scanner: %v", scanErr)
	}

	if err := cmd.Wait(); err != nil {
		return "", TrackMeta{}, "", fmt.Errorf("yt-dlp: %v", err)
	}

	// Populate ytMeta
	var ytMeta TrackMeta
	if ytArtist != "" && !strings.EqualFold(ytArtist, "na") {
		ytMeta.Artist = ytArtist
	}
	if ytAlbum != "" && !strings.EqualFold(ytAlbum, "na") {
		ytMeta.Album = ytAlbum
	}
	if ytTrack != "" && !strings.EqualFold(ytTrack, "na") {
		ytMeta.Title = ytTrack
	} else if videoTitle != "" {
		ytMeta.Title = videoTitle
	}
	if ytYear != "" && !strings.EqualFold(ytYear, "na") {
		ytMeta.Date = ytYear
	}

	// Secondary fallback split on video title if artist is still missing
	if ytMeta.Artist == "" && videoTitle != "" {
		fallback := parseFallbackMeta(videoTitle)
		ytMeta.Artist = fallback.Artist
		if ytMeta.Title == "" {
			ytMeta.Title = fallback.Title
		}
	}

	if ytMeta.Title != "" {
		t.SetTitle(ytMeta.Title)
	}
	if ytMeta.Artist != "" {
		t.SetArtist(ytMeta.Artist)
	}

	// Glob for the downloaded file. yt-dlp may leave .part files on partial
	// downloads, so we only accept known audio container extensions.
	pattern := filepath.Join(os.TempDir(), fmt.Sprintf("rytm_%s.*", t.ID))
	matches, globErr := filepath.Glob(pattern)
	if globErr != nil {
		return "", TrackMeta{}, "", fmt.Errorf("glob: %v", globErr)
	}
	for _, p := range matches {
		if isAudioFile(p) {
			return p, ytMeta, videoTitle, nil
		}
	}
	return "", TrackMeta{}, "", fmt.Errorf("no audio file found in temp dir (task %s)", t.ID)
}

// isAudioFile returns false for file extensions that yt-dlp writes as
// side-car files during download (progress state, thumbnail, metadata, etc.).
func isAudioFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".part", ".ytdl", ".jpg", ".jpeg", ".png", ".webp", ".json", ".description":
		return false
	}
	return true
}

// transcodeWithMeta runs a single FFmpeg pass that:
//   - converts the raw audio to AAC at 256 kbps,
//   - applies the -10 dB volume normalisation filter,
//   - embeds all available MusicBrainz metadata fields,
//   - attaches the cover art image (when present) as an attached picture.
//
// No third-party tag-editing library is used; all metadata injection is
// performed by writing a UTF-8 ffmetadata file and mapping it.
func (m *Manager) transcodeWithMeta(ctx context.Context, rawPath, coverPath string, meta TrackMeta, outPath string, taskID string) error {
	hasCover := coverPath != ""

	// Build the ffmetadata file content
	var sb strings.Builder
	sb.WriteString(";FFMETADATA1\r\n")
	if meta.Title != "" {
		sb.WriteString("title=");
		sb.WriteString(escapeMetadataValue(meta.Title));
		sb.WriteString("\r\n")
	}
	if meta.Artist != "" {
		sb.WriteString("artist=");
		sb.WriteString(escapeMetadataValue(meta.Artist));
		sb.WriteString("\r\n")
	}
	if meta.Album != "" {
		sb.WriteString("album=");
		sb.WriteString(escapeMetadataValue(meta.Album));
		sb.WriteString("\r\n")
	}
	if meta.Date != "" {
		sb.WriteString("date=");
		sb.WriteString(escapeMetadataValue(meta.Date));
		sb.WriteString("\r\n")
	}
	if meta.TrackNum != "" {
		sb.WriteString("track=");
		sb.WriteString(escapeMetadataValue(meta.TrackNum));
		sb.WriteString("\r\n")
	}
	if meta.Genre != "" {
		sb.WriteString("genre=");
		sb.WriteString(escapeMetadataValue(meta.Genre));
		sb.WriteString("\r\n")
	}

	// Write metadata file to temp dir
	metaPath := filepath.Join(os.TempDir(), fmt.Sprintf("rytm_%s_metadata.txt", taskID))
	if err := os.WriteFile(metaPath, []byte(sb.String()), 0644); err != nil {
		return fmt.Errorf("write metadata file: %v", err)
	}
	defer os.Remove(metaPath)

	args := []string{"-y", "-i", rawPath}
	if hasCover {
		args = append(args, "-i", coverPath)
	}
	args = append(args, "-i", metaPath)

	// Explicit stream mapping
	args = append(args, "-map", "0:a")
	if hasCover {
		args = append(args, "-map", "1:v")
	}

	// Map metadata from the metadata file
	metaIdx := 1
	if hasCover {
		metaIdx = 2
	}
	args = append(args, "-map_metadata", strconv.Itoa(metaIdx))

	// Audio codec and bitrate.
	args = append(args, "-c:a", "aac", "-b:a", "256k")

	// Cover art: copy the JPEG bytes into the mp4/m4a container as-is.
	if hasCover {
		args = append(args, "-c:v", "copy")
	}

	// Volume normalisation (same filter as the previous single-step pipeline).
	args = append(args, "-af", "volume=-10dB")

	// Mark the image stream as an attached picture so media players treat it
	// as cover art rather than a video track.
	if hasCover {
		args = append(args, "-disposition:v:0", "attached_pic")
	}

	args = append(args, outPath)

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Dir = "./"
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// findDownloadedThumbnail looks for downloaded video thumbnail variants in temp folder.
func findDownloadedThumbnail(taskID string) string {
	for _, ext := range []string{".jpg", ".jpeg", ".png", ".webp"} {
		path := filepath.Join(os.TempDir(), fmt.Sprintf("rytm_%s%s", taskID, ext))
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}

// escapeMetadataValue escapes special characters for the ffmetadata format.
func escapeMetadataValue(s string) string {
	r := strings.NewReplacer(
		"\\", "\\\\",
		"=", "\\=",
		";", "\\;",
		"#", "\\#",
		"\n", "\\\n",
	)
	return r.Replace(s)
}

// parseFallbackMeta reconstructs metadata fields from the YouTube video title.
func parseFallbackMeta(videoTitle string) TrackMeta {
	meta := TrackMeta{
		Title:  videoTitle,
		Artist: "Unknown Artist",
		Album:  "YouTube",
	}

	// Try to split on " - " (dash) to separate artist and title
	parts := strings.SplitN(videoTitle, " - ", 2)
	if len(parts) == 2 {
		meta.Artist = strings.TrimSpace(parts[0])
		meta.Title = strings.TrimSpace(parts[1])
	} else {
		// Also try split on " – " (en dash) or " — " (em dash) or " : "
		for _, dash := range []string{" – ", " — ", " : "} {
			parts = strings.SplitN(videoTitle, dash, 2)
			if len(parts) == 2 {
				meta.Artist = strings.TrimSpace(parts[0])
				meta.Title = strings.TrimSpace(parts[1])
				break
			}
		}
	}

	// Clean up any common YouTube suffixes like "(Official Video)", "feat.", "[Audio]", etc.
	meta.Title = cleanTitle(meta.Title)
	meta.Artist = cleanTitle(meta.Artist)
	return meta
}

// cleanTitle removes brackets containing official/video/audio indicators to clean up titles.
func cleanTitle(s string) string {
	re := regexp.MustCompile(`(?i)\s*[\(\[][^\]\)]*(official|video|audio|lyric|clip|hq|hd|remastered|mono|stereo)[^\]\)]*[\)\]]`)
	reFeat := regexp.MustCompile(`(?i)\s*feat\..*`)
	s = re.ReplaceAllString(s, "")
	s = reFeat.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

// buildOutputName constructs the final .m4a filename from MusicBrainz metadata,
// falling back to the extracted video title or raw query string when the fingerprinting stage produced
// no usable data. The file name should be just the original song title (without the artist).
func buildOutputName(meta TrackMeta, videoTitle string, query string) string {
	title := sanitizeFilename(meta.Title)
	if title != "" {
		return title + ".m4a"
	}

	vTitle := sanitizeFilename(videoTitle)
	if vTitle != "" {
		return vTitle + ".m4a"
	}

	return sanitizeFilename(query) + ".m4a"
}

// sanitizeFilename removes or replaces characters that are illegal in Windows
// and POSIX filenames so the output path is always valid.
func sanitizeFilename(s string) string {
	r := strings.NewReplacer(
		"/", "-", "\\", "-", ":", " -",
		"*", "", "?", "", "\"", "'",
		"<", "", ">", "", "|", "-",
	)
	return strings.TrimSpace(r.Replace(s))
}

// ─── Manager helpers (unchanged) ─────────────────────────────────────────────

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

func (m *Manager) TestDownloadRaw(t *Task) (string, TrackMeta, string, error) {
	return m.downloadRaw(context.Background(), t)
}

func TestParseFallbackMeta(title string) TrackMeta {
	return parseFallbackMeta(title)
}
