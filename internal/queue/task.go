package queue

import (
	"context"
	"os/exec"
	"rytm/internal/ipc"
	"sync"
)

type Task struct {
	ID               string
	SessionID        string
	Query            string
	ResolvedURL      string // Set by the resolution engine; used instead of Query for yt-dlp
	Title            string
	Artist           string
	Album            string
	PlaylistName     string
	Status           string // "Queued", "Pending", "Resolving", "Downloading", "Fingerprinting", "Tagging", "Done", "Failed", "Cancelled", "Fetching Playlist"
	Error            string
	OutputDir        string
	IsPlaylist       bool
	PlaylistTrackNum int
	Cmd              *exec.Cmd
	CancelFn         context.CancelFunc
	mu               sync.RWMutex
}

func (t *Task) GetStatus() ipc.TaskStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return ipc.TaskStatus{
		TaskID:           t.ID,
		SessionID:        t.SessionID,
		Query:            t.Query,
		Title:            t.Title,
		Artist:           t.Artist,
		Album:            t.Album,
		Status:           t.Status,
		Error:            t.Error,
		IsPlaylist:       t.IsPlaylist,
		PlaylistName:     t.PlaylistName,
		PlaylistTrackNum: t.PlaylistTrackNum,
	}
}
func (t *Task) SetStatus(status string, errMsg string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Status = status
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
func (t *Task) SetAlbum(album string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Album = album
}
