package queue

import (
	"context"
	"os/exec"
	"rytm/internal/ipc"
	"sync"
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