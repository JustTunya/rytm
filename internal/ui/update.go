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

// resolutionConfidentMsg is sent when the resolver has high confidence — auto-queue.
type resolutionConfidentMsg struct {
	URL    string
	Title  string
	Artist string
}

// resolutionDisambiguateMsg is sent when the resolver needs user input.
type resolutionDisambiguateMsg struct {
	Candidates []ipc.ResolveCandidate
}

type statusUpdateMsg struct {
	Tasks []ipc.TaskStatus
}
type tickMsg struct{}

// FIX 1: Use pointer receiver (*Model) so state updates persist
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.WindowWidth = msg.Width
		m.WindowHeight = msg.Height
		m.TextInput.Width = m.WindowWidth - 10
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "q":
			if m.State != StateInput {
				return m, tea.Quit
			}
		case "esc":
			if m.State == StateDashboard || m.State == StateDisambiguation {
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

				// Determine if we should submit immediately or resolve first
				if isDirectURL(m.SearchQuery) {
					return m, sendSubmitURLCmd(m.SearchQuery)
				}
				return m, sendResolveCmd(m.SearchQuery)
			}
			if m.State == StateDisambiguation && len(m.DisambiguationItems) > 0 {
				selected := m.DisambiguationItems[m.DisambiguationCursor]
				m.State = StateSearching
				m.DisambiguationItems = nil
				return m, sendSubmitURLCmd(selected.URL)
			}
		case "up":
			if m.State == StateDashboard && m.ScrollOffset > 0 {
				m.ScrollOffset--
			}
			if m.State == StateDisambiguation && m.DisambiguationCursor > 0 {
				m.DisambiguationCursor--
			}
			return m, nil
		case "down":
			if m.State == StateDashboard {
				m.ScrollOffset++
			}
			if m.State == StateDisambiguation && m.DisambiguationCursor < len(m.DisambiguationItems)-1 {
				m.DisambiguationCursor++
			}
			return m, nil
		}

	case downloadStartedMsg:
		m.State = StateDashboard
		m.CurrentSessionID = msg.TaskID
		return m, pollStatusCmd()

	case resolutionConfidentMsg:
		m.State = StateSearching
		return m, sendSubmitURLCmd(msg.URL)

	case resolutionDisambiguateMsg:
		m.State = StateDisambiguation
		m.DisambiguationCursor = 0
		m.DisambiguationItems = make([]DisambiguationItem, 0, len(msg.Candidates))
		for _, c := range msg.Candidates {
			m.DisambiguationItems = append(m.DisambiguationItems, DisambiguationItem{
				URL: c.URL, Title: c.Title, Artist: c.Artist, Album: c.Album,
				Type: c.Type, Score: c.Score,
			})
		}
		return m, nil

	case statusUpdateMsg:
		m.Tracks = make([]TrackItem, 0, len(msg.Tasks))
		anyRunning := false
		for _, t := range msg.Tasks {
			if t.SessionID != m.CurrentSessionID {
				continue
			}
			m.Tracks = append(m.Tracks, TrackItem{
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
			})

			// Use t.Status instead of t.State
			if t.Status == "Pending" || t.Status == "Resolving" || t.Status == "Downloading" || t.Status == "Fingerprinting" || t.Status == "Tagging" || t.Status == "Queued" || t.Status == "Fetching Playlist" {
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

func sendResolveCmd(query string) tea.Cmd {
	return func() tea.Msg {
		// FIX 3: Use dynamic Windows/Linux socket path
		conn, err := net.Dial("unix", ipc.SocketPath)
		if err != nil {
			return errMsg(fmt.Errorf("failed to connect to rytmd: %w", err))
		}
		defer conn.Close()

		req := ipc.Request{
			Command: ipc.CmdResolve,
			Query:   query,
		}

		if err := json.NewEncoder(conn).Encode(req); err != nil {
			return errMsg(fmt.Errorf("failed to send resolve request: %w", err))
		}

		var resp ipc.Response
		if err := json.NewDecoder(conn).Decode(&resp); err != nil {
			return errMsg(fmt.Errorf("failed to decode resolve response: %w", err))
		}

		if !resp.Success {
			return errMsg(fmt.Errorf("resolve failed: %s", resp.Error))
		}

		if resp.Resolve != nil && resp.Resolve.Confident && len(resp.Resolve.Candidates) > 0 {
			top := resp.Resolve.Candidates[0]
			return resolutionConfidentMsg{URL: top.URL, Title: top.Title, Artist: top.Artist}
		}
		return resolutionDisambiguateMsg{Candidates: resp.Resolve.Candidates}
	}
}

func sendSubmitURLCmd(url string) tea.Cmd {
	return func() tea.Msg {
		// FIX 3: Use dynamic Windows/Linux socket path
		conn, err := net.Dial("unix", ipc.SocketPath)
		if err != nil {
			return errMsg(fmt.Errorf("failed to connect to rytmd: %w", err))
		}
		defer conn.Close()

		req := ipc.Request{
			Command: ipc.CmdSubmitURL,
			Query:   url,
		}

		if err := json.NewEncoder(conn).Encode(req); err != nil {
			return errMsg(fmt.Errorf("failed to send submit request: %w", err))
		}

		var resp ipc.Response
		if err := json.NewDecoder(conn).Decode(&resp); err != nil {
			return errMsg(fmt.Errorf("failed to decode submit response: %w", err))
		}

		if !resp.Success {
			return errMsg(fmt.Errorf("submit command failed: %s", resp.Error))
		}

		return downloadStartedMsg{TaskID: resp.TaskID}
	}
}

func isDirectURL(query string) bool {
	return len(query) > 4 && (query[:4] == "http")
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
