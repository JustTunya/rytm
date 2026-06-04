package ui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Internal message definitions
type errMsg error
type searchResultMsg struct{}
type downloadCompleteMsg struct{ index int }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
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
				
				// Simulate an async network request for now
				return m, fakeSearchAction()
			}
		}

	case searchResultMsg:
		// Move to dashboard and append a mock download task
		m.State = StateDashboard
		m.Tracks = append(m.Tracks, TrackItem{
			Title:    m.SearchQuery,
			Status:   "Downloading",
			Progress: 0.1,
		})
		return m, fakeDownloadProgress(len(m.Tracks) - 1)

	case downloadCompleteMsg:
		if msg.index < len(m.Tracks) {
			m.Tracks[msg.index].Status = "Done"
			m.Tracks[msg.index].Progress = 1.0
		}
		return m, nil

	case errMsg:
		m.Err = msg
		return m, nil
	}

	// Route updates to sub-components when in input mode
	if m.State == StateInput {
		m.TextInput, cmd = m.TextInput.Update(msg)
	}
	return m, cmd
}

// Visual placeholders for your future yt-dlp backend functionality
func fakeSearchAction() tea.Cmd {
	return tea.Tick(time.Second*1, func(t time.Time) tea.Msg {
		return searchResultMsg{}
	})
}

func fakeDownloadProgress(index int) tea.Cmd {
	return tea.Tick(time.Second*2, func(t time.Time) tea.Msg {
		return downloadCompleteMsg{index: index}
	})
}