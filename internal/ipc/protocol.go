package ipc

import (
	"os"
	"path/filepath"
)

var SocketPath = filepath.Join(os.TempDir(), "rytm.sock")

type CommandType string

const (
	CmdDownload CommandType = "DOWNLOAD"
	CmdStatus   CommandType = "STATUS"
	CmdCancel   CommandType = "CANCEL"
)

type Request struct {
	Command CommandType `json:"command"`
	Query   string      `json:"query,omitempty"`   // Query or URL to download
	TaskID  string      `json:"task_id,omitempty"` // Specific task ID to check/cancel
}

type TaskStatus struct {
	TaskID           string `json:"task_id"`
	SessionID        string `json:"session_id"`
	Query            string `json:"query"`
	Title            string `json:"title"`
	Artist           string `json:"artist"`
	Album            string `json:"album"`
	Status           string `json:"status"` // "Pending", "Downloading", "Tagging", "Done", "Failed", "Cancelled"
	Error            string `json:"error,omitempty"`
	IsPlaylist       bool   `json:"is_playlist"`
	PlaylistName     string `json:"playlist_name,omitempty"`
	PlaylistTrackNum int    `json:"playlist_track_num,omitempty"`
}

type Response struct {
	Success bool         `json:"success"`
	TaskID  string       `json:"task_id,omitempty"`
	Status  *TaskStatus  `json:"status,omitempty"`
	Tasks   []TaskStatus `json:"tasks,omitempty"` // Can return all tasks if no specific task ID was queried
	Error   string       `json:"error,omitempty"`
}
