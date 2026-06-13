package queue

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Precompile regexes for yt-dlp stdout scanning to optimize performance and avoid allocation overhead.
var (
	rePostProc = regexp.MustCompile(`\[(ExtractAudio|Metadata|Thumbnails|FixupM4a)\]`)
	reTitle    = regexp.MustCompile(`^rytm_title:(.*)$`)
	reArtist   = regexp.MustCompile(`^rytm_artist:(.*)$`)
	reAlbum    = regexp.MustCompile(`^rytm_album:(.*)$`)
	reTrack    = regexp.MustCompile(`^rytm_track:(.*)$`)
	reYear     = regexp.MustCompile(`^rytm_release_year:(.*)$`)
)

func (m *Manager) downloadRaw(ctx context.Context, t *Task) (string, TrackMeta, string, error) {
	queryArg := t.Query
	t.mu.RLock()
	resolvedURL := t.ResolvedURL
	t.mu.RUnlock()
	
	if resolvedURL != "" {
		queryArg = resolvedURL
	} else if !strings.HasPrefix(t.Query, "http://") && !strings.HasPrefix(t.Query, "https://") {
		queryArg = fmt.Sprintf(`ytsearch1:%s "Provided to YouTube"`, strings.TrimSpace(t.Query))
	}
	rawTemplate := filepath.Join(os.TempDir(), fmt.Sprintf("rytm_%s.%%(ext)s", t.ID))
	args := []string{
		"--cookies", "cookies.txt",
		queryArg,
		"--no-playlist",
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
	dlCtx, dlCancel := context.WithTimeout(ctx, 10*time.Minute)
	defer dlCancel()
	cmd := exec.CommandContext(dlCtx, "yt-dlp", args...)
	cmd.Dir = "./"
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8", "PYTHONUTF8=1")
	t.mu.Lock()
	t.Cmd = cmd
	t.mu.Unlock()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", TrackMeta{}, "", fmt.Errorf("stdout pipe: %v", err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return "", TrackMeta{}, "", fmt.Errorf("yt-dlp start: %v", err)
	}
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
		case rePostProc.MatchString(line):
			t.SetStatus("Downloading", "")
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		_ = cmd.Wait()
		return "", TrackMeta{}, "", fmt.Errorf("scanner: %v", scanErr)
	}
	if err := cmd.Wait(); err != nil {
		return "", TrackMeta{}, "", fmt.Errorf("yt-dlp: %v", err)
	}
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
	if ytMeta.Album != "" {
		t.SetAlbum(ytMeta.Album)
	}
	// Optimize search by looking up expected extension patterns directly.
	// This avoids costly and slow directory globbing of TempDir on Windows/large directories.
	audioPath, findErr := findAudioFile(t.ID)
	if findErr != nil {
		return "", TrackMeta{}, "", findErr
	}
	return audioPath, ytMeta, videoTitle, nil
}
func findAudioFile(taskID string) (string, error) {
	// Try common audio extensions directly to avoid slow Glob on large temp dirs
	extensions := []string{"webm", "m4a", "opus", "mp3", "wav", "aac", "ogg", "flac"}
	for _, ext := range extensions {
		p := filepath.Join(os.TempDir(), fmt.Sprintf("rytm_%s.%s", taskID, ext))
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, nil
		}
	}
	// Fallback to Glob if it's some other extension
	pattern := filepath.Join(os.TempDir(), fmt.Sprintf("rytm_%s.*", taskID))
	matches, globErr := filepath.Glob(pattern)
	if globErr != nil {
		return "", fmt.Errorf("glob: %v", globErr)
	}
	for _, p := range matches {
		if isAudioFile(p) {
			return p, nil
		}
	}
	return "", fmt.Errorf("no audio file found in temp dir (task %s)", taskID)
}
func isAudioFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".part", ".ytdl", ".jpg", ".jpeg", ".png", ".webp", ".json", ".description":
		return false
	}
	return true
}
func findDownloadedThumbnail(taskID string) string {
	for _, ext := range []string{".jpg", ".jpeg", ".png", ".webp"} {
		path := filepath.Join(os.TempDir(), fmt.Sprintf("rytm_%s%s", taskID, ext))
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}
func (m *Manager) TestDownloadRaw(t *Task) (string, TrackMeta, string, error) {
	return m.downloadRaw(context.Background(), t)
}
