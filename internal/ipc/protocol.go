package ipc

import (
	"os"
	"path/filepath"
	"runtime"
)

var SocketPath string

func init() {
	if runtime.GOOS == "windows" {
		SocketPath = filepath.Join(os.TempDir(), "rytm_v2.sock")
	} else {
		SocketPath = "/tmp/rytm_v2.sock"
	}
}

type CommandType string

const (
	CmdDownload  CommandType = "DOWNLOAD"
	CmdStatus    CommandType = "STATUS"
	CmdCancel    CommandType = "CANCEL"
	CmdResolve   CommandType = "RESOLVE"
	CmdSubmitURL CommandType = "SUBMIT_URL"
)

type Request struct {
	Command CommandType `json:"command"`
	TaskID  string      `json:"task_id,omitempty"`
	Query   string      `json:"query,omitempty"`
}

type TaskStatus struct {
	TaskID           string  `json:"task_id"`
	SessionID        string  `json:"session_id"`
	Query            string  `json:"query"`
	Title            string  `json:"title"`
	Artist           string  `json:"artist"`
	Album            string  `json:"album"`
	Status           string  `json:"status"` // "Pending", "Resolving", "Downloading", "Tagging", "Done", "Failed", "Cancelled"
	Error            string  `json:"error,omitempty"`
	IsPlaylist       bool    `json:"is_playlist"`
	PlaylistName     string  `json:"playlist_name,omitempty"`
	PlaylistTrackNum int     `json:"playlist_track_num,omitempty"`
}

type Response struct {
	Success bool             `json:"success"`
	TaskID  string           `json:"task_id,omitempty"`
	Status  *TaskStatus      `json:"status,omitempty"`
	Tasks   []TaskStatus     `json:"tasks,omitempty"`
	Resolve *ResolveResponse `json:"resolve,omitempty"`
	Error   string           `json:"error,omitempty"`
}

type ResolveCandidate struct {
	URL         string  `json:"url"`
	Title       string  `json:"title"`
	Artist      string  `json:"artist"`
	Album       string  `json:"album"`
	Type        string  `json:"type"` // "Song", "Album", "Video", "Playlist"
	Score       float64 `json:"score"`
}

type ResolveResponse struct {
	Confident  bool               `json:"confident"`
	Candidates []ResolveCandidate `json:"candidates"`
}
