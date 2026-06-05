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
	TaskID   string `json:"task_id"`
	Query    string `json:"query"`
	Title    string `json:"title"`
	Artist   string `json:"artist"`
	Status   string `json:"status"`   // "Pending", "Downloading", "Tagging", "Done", "Failed", "Cancelled"
	Progress int    `json:"progress"` // 0 to 100
	Error    string `json:"error,omitempty"`
}

type Response struct {
	Success bool         `json:"success"`
	TaskID  string       `json:"task_id,omitempty"`
	Status  *TaskStatus  `json:"status,omitempty"`
	Tasks   []TaskStatus `json:"tasks,omitempty"` // Can return all tasks if no specific task ID was queried
	Error   string       `json:"error,omitempty"`
}
