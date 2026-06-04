package queue

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"rytm/internal/ipc"
)

type Task struct {
	ID       string
	Query    string
	Status   string // "Pending", "Downloading", "Tagging", "Done", "Failed", "Cancelled"
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
		Status:   "Pending",
		Progress: 0,
		CancelFn: cancel,
	}
	m.tasks[taskID] = task

	go m.runTask(ctx, task)

	return taskID
}

func (m *Manager) runTask(ctx context.Context, t *Task) {
	t.SetStatus("Downloading", 0, "")

	queryArg := t.Query
	if !strings.HasPrefix(t.Query, "http://") && !strings.HasPrefix(t.Query, "https://") {
		queryArg = "ytsearch:" + t.Query
	}

	args := []string{
		"-I", "1",
		queryArg,
		"-x",
		"-f", "bestaudio/best",
		"--audio-format", "m4a",
		"--embed-metadata",
		"--embed-thumbnail",
		"-o", "%(artist)s - %(title)s.%(ext)s",
	}

	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
	cmd.Dir = "./" // Set working directory to project root

	t.mu.Lock()
	t.Cmd = cmd
	t.mu.Unlock()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.SetStatus("Failed", 0, fmt.Sprintf("failed to get stdout: %v", err))
		return
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		t.SetStatus("Failed", 0, fmt.Sprintf("failed to start yt-dlp: %v", err))
		return
	}

	reProgress := regexp.MustCompile(`\[download\]\s+([0-9.]+)%`)
	reTagging := regexp.MustCompile(`\[(ExtractAudio|Metadata|Thumbnails)\]`)

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()

		if matches := reProgress.FindStringSubmatch(line); len(matches) > 1 {
			if pctFloat, err := strconv.ParseFloat(matches[1], 64); err == nil {
				pct := int(pctFloat)
				if pct < 100 {
					t.SetStatus("Downloading", pct, "")
				} else {
					t.SetStatus("Tagging", 99, "")
				}
			}
		} else if reTagging.MatchString(line) {
			t.SetStatus("Tagging", 99, "")
		}
	}

	if err := scanner.Err(); err != nil {
		t.SetStatus("Failed", t.Progress, fmt.Sprintf("scanner error: %v", err))
		return
	}

	err = cmd.Wait()

	select {
	case <-ctx.Done():
		t.SetStatus("Cancelled", t.Progress, "Download cancelled by user")
		return
	default:
	}

	if err != nil {
		t.SetStatus("Failed", t.Progress, fmt.Sprintf("yt-dlp error: %v", err))
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
