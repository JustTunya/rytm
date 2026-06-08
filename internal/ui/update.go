package ui

import (
	"encoding/json"
	"fmt"
	"net"
	"time"

	"rytm/internal/ipc"

	tea "github.com/charmbracelet/bubbletea"
)

// Internal message definitions
type errMsg error
type downloadStartedMsg struct {
	TaskID string
}
type statusUpdateMsg struct {
	Tasks []ipc.TaskStatus
}
type tickMsg struct{}

// FIX 1: Use pointer receiver (*Model) so state updates persist
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "q":
			if m.State != StateInput {
				return m, tea.Quit
			}
		case "esc":
			if m.State == StateDashboard {
				m.State = StateInput
				m.TextInput.Focus()
				m.TextInput.SetValue("")
			}
			return m, nil
		case "enter":
			if m.State == StateInput && m.TextInput.Value() != "" {
				m.SearchQuery = m.TextInput.Value()
				m.State = StateSearching
				m.TextInput.Blur()
				m.Err = nil
				m.ScrollOffset = 0 // Reset scroll offset on new search
				return m, sendDownloadCmd(m.SearchQuery)
			}
		case "up":
			if m.State == StateDashboard && m.ScrollOffset > 0 {
				m.ScrollOffset--
			}
			return m, nil
		case "down":
			if m.State == StateDashboard {
				m.ScrollOffset++
			}
			return m, nil
		}

	case downloadStartedMsg:
		m.State = StateDashboard
		return m, pollStatusCmd()

	case statusUpdateMsg:
		m.Tracks = make([]TrackItem, len(msg.Tasks))
		anyRunning := false
		for i, t := range msg.Tasks {
			m.Tracks[i] = TrackItem{
				TaskID:           t.TaskID,
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
			
			// Use t.Status instead of t.State
			if t.Status == "Pending" || t.Status == "Downloading" || t.Status == "Fingerprinting" || t.Status == "Tagging" || t.Status == "Queued" || t.Status == "Fetching Playlist" {
				anyRunning = true
			}
		}
		
		if anyRunning && m.State == StateDashboard {
			return m, tickPoll()
		}
		return m, nil

	case tickMsg:
		m.FrameIndex = (m.FrameIndex + 1) % 10
		if m.State == StateDashboard {
			m.TickCount++
			if m.TickCount >= 4 {
				m.TickCount = 0
				return m, pollStatusCmd()
			}
			return m, tickPoll()
		}
		return m, nil

	case errMsg:
		m.Err = msg
		if m.State == StateSearching {
			m.State = StateInput
			m.TextInput.Focus()
		}
		return m, nil
	}

	// Route updates to sub-components when in input mode
	if m.State == StateInput {
		m.TextInput, cmd = m.TextInput.Update(msg)
	}
	return m, cmd
}

func sendDownloadCmd(query string) tea.Cmd {
	return func() tea.Msg {
		// FIX 3: Use dynamic Windows/Linux socket path
		conn, err := net.Dial("unix", ipc.SocketPath)
		if err != nil {
			return errMsg(fmt.Errorf("failed to connect to rytmd: %w", err))
		}
		defer conn.Close()

		req := ipc.Request{
			Command: ipc.CmdDownload,
			Query:   query,
		}

		if err := json.NewEncoder(conn).Encode(req); err != nil {
			return errMsg(fmt.Errorf("failed to send download request: %w", err))
		}

		var resp ipc.Response
		if err := json.NewDecoder(conn).Decode(&resp); err != nil {
			return errMsg(fmt.Errorf("failed to decode download response: %w", err))
		}

		if !resp.Success {
			return errMsg(fmt.Errorf("download command failed: %s", resp.Error))
		}

		return downloadStartedMsg{TaskID: resp.TaskID}
	}
}

func pollStatusCmd() tea.Cmd {
	return func() tea.Msg {
		// FIX 3: Use dynamic Windows/Linux socket path
		conn, err := net.Dial("unix", ipc.SocketPath)
		if err != nil {
			return errMsg(fmt.Errorf("failed to connect to rytmd: %w", err))
		}
		defer conn.Close()

		req := ipc.Request{
			Command: ipc.CmdStatus,
		}

		if err := json.NewEncoder(conn).Encode(req); err != nil {
			return errMsg(fmt.Errorf("failed to send status request: %w", err))
		}

		var resp ipc.Response
		if err := json.NewDecoder(conn).Decode(&resp); err != nil {
			return errMsg(fmt.Errorf("failed to decode status response: %w", err))
		}

		if !resp.Success {
			return errMsg(fmt.Errorf("status command failed: %s", resp.Error))
		}

		return statusUpdateMsg{Tasks: resp.Tasks}
	}
}

func tickPoll() tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg{}
	})
}