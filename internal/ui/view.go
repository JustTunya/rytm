package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	// Minimalist styling components matching a crisp terminal look
	accentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff")).Bold(true)
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#555555"))
	greyStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
	borderStyle = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("#333333")).Padding(1, 2)

	spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
)

func (m Model) View() string {
	var doc strings.Builder

	doc.WriteString(accentStyle.Render("RYTM // Media Ingestion Engine"))
	doc.WriteString("\n\n")

	switch m.State {
	case StateInput:
		doc.WriteString("Search Index / Ingest Stream:\n")
		doc.WriteString(m.TextInput.View())
		doc.WriteString("\n\n")
		doc.WriteString(dimStyle.Render("Press [q] or [Ctrl+C] to exit."))

	case StateSearching:
		doc.WriteString(fmt.Sprintf("Searching YouTube Music for: '%s'...\n\n", m.SearchQuery))
		doc.WriteString(dimStyle.Render("Resolving pristine topic stream stream..."))

	case StateDashboard:
		doc.WriteString("Active Ingestion Queue:\n\n")

		var singles []TrackItem
		playlists := make(map[string][]TrackItem)
		var playlistOrder []string

		for _, t := range m.Tracks {
			if t.IsPlaylist && t.PlaylistTrackNum > 0 {
				if _, ok := playlists[t.PlaylistName]; !ok {
					playlistOrder = append(playlistOrder, t.PlaylistName)
				}
				playlists[t.PlaylistName] = append(playlists[t.PlaylistName], t)
			} else {
				// Don't show the main playlist task if it's already done and spawned children
				if t.IsPlaylist && t.PlaylistTrackNum == 0 && t.Status == "Done" {
					continue
				}
				singles = append(singles, t)
			}
		}

		var flatItems []TrackItem
		flatItems = append(flatItems, singles...)
		
		for _, pName := range playlistOrder {
			pTracks := playlists[pName]
			sort.Slice(pTracks, func(i, j int) bool {
				return pTracks[i].PlaylistTrackNum < pTracks[j].PlaylistTrackNum
			})
			flatItems = append(flatItems, pTracks...)
		}

		if len(flatItems) == 0 {
			doc.WriteString(dimStyle.Render("Queue is empty."))
		} else {
			maxOffset := len(flatItems) - 6
			if maxOffset < 0 {
				maxOffset = 0
			}
			
			offset := m.ScrollOffset
			if offset > maxOffset {
				offset = maxOffset
			}
			if offset < 0 {
				offset = 0
			}

			start := offset
			end := start + 6
			if end > len(flatItems) {
				end = len(flatItems)
			}
			displayItems := flatItems[start:end]

			lastPlaylist := ""
			// If we scroll into the middle of a playlist, show its header at the top
			if len(displayItems) > 0 && displayItems[0].IsPlaylist && displayItems[0].PlaylistTrackNum > 0 {
				lastPlaylist = displayItems[0].PlaylistName
				doc.WriteString(accentStyle.Render("Playlist: " + lastPlaylist) + "\n")
				numStyle := lipgloss.NewStyle().Width(4)
				titleStyle := lipgloss.NewStyle().Width(35)
				artistStyle := lipgloss.NewStyle().Width(20)
				albumStyle := lipgloss.NewStyle().Width(20)
				statusStyle := lipgloss.NewStyle().Width(20)
				header := numStyle.Render("#") + titleStyle.Render("Title") + artistStyle.Render("Artist") + albumStyle.Render("Album") + statusStyle.Render("Status")
				doc.WriteString(dimStyle.Render(header) + "\n")
			}

			for i, track := range displayItems {
				if track.IsPlaylist && track.PlaylistTrackNum > 0 {
					if track.PlaylistName != lastPlaylist {
						if i > 0 {
							doc.WriteString("\n")
						}
						doc.WriteString(accentStyle.Render("Playlist: " + track.PlaylistName) + "\n")
						numStyle := lipgloss.NewStyle().Width(4)
						titleStyle := lipgloss.NewStyle().Width(35)
						artistStyle := lipgloss.NewStyle().Width(20)
						albumStyle := lipgloss.NewStyle().Width(20)
						statusStyle := lipgloss.NewStyle().Width(20)
						header := numStyle.Render("#") + titleStyle.Render("Title") + artistStyle.Render("Artist") + albumStyle.Render("Album") + statusStyle.Render("Status")
						doc.WriteString(dimStyle.Render(header) + "\n")
						lastPlaylist = track.PlaylistName
					}

					statusSymbol := "⏳"
					isPendingOrRunning := track.Status == "Pending" || track.Status == "Downloading" || track.Status == "Fingerprinting" || track.Status == "Tagging" || track.Status == "Queued" || track.Status == "Fetching Playlist"
					if isPendingOrRunning {
						statusSymbol = spinnerFrames[m.FrameIndex%len(spinnerFrames)]
					} else if track.Status == "Done" { 
						statusSymbol = "✓" 
					} else if track.Status == "Failed" || track.Status == "Cancelled" { 
						statusSymbol = "✗" 
					}
					
					statusText := track.Status
					if track.Status == "Failed" && track.Error != "" {
						statusText = "Failed"
					}
					
					title := track.Title
					if title == "" { 
						title = "Track " + fmt.Sprintf("%d", track.PlaylistTrackNum)
					}
					if len(title) > 33 { title = title[:30] + "..." }
					
					artist := track.Artist
					if len(artist) > 18 { artist = artist[:15] + "..." }

					album := track.Album
					if len(album) > 18 { album = album[:15] + "..." }
					
					numStr := fmt.Sprintf("%d", track.PlaylistTrackNum)
					
					numStyle := lipgloss.NewStyle().Width(4)
					titleStyle := lipgloss.NewStyle().Width(35)
					artistStyle := lipgloss.NewStyle().Width(20)
					albumStyle := lipgloss.NewStyle().Width(20)
					statusStyle := lipgloss.NewStyle().Width(20)

					row := numStyle.Render(numStr) + 
						   titleStyle.Render(title) + 
						   artistStyle.Render(artist) + 
						   albumStyle.Render(album) + 
						   statusStyle.Render(statusSymbol + " " + statusText)
					
					doc.WriteString(row + "\n")
				} else {
					lastPlaylist = "" // Reset so next playlist prints its header
					
					isPendingOrRunning := track.Status == "Pending" || track.Status == "Downloading" || track.Status == "Fingerprinting" || track.Status == "Tagging" || track.Status == "Queued" || track.Status == "Fetching Playlist"
					
					statusSymbol := "⏳"
					if isPendingOrRunning {
						statusSymbol = spinnerFrames[m.FrameIndex%len(spinnerFrames)]
					} else if track.Status == "Done" {
						statusSymbol = "✓"
					} else if track.Status == "Failed" || track.Status == "Cancelled" {
						statusSymbol = "✗"
					}

					statusText := track.Status
					if track.Status == "Failed" && track.Error != "" {
						statusText = fmt.Sprintf("Failed: %s", track.Error)
					}

					if isPendingOrRunning && (track.Title == "" || track.Artist == "") {
						doc.WriteString(fmt.Sprintf("%s Searching for %s... [%s]\n", statusSymbol, track.Query, statusText))
					} else {
						title := track.Title
						if title == "" {
							title = track.Query
						}
						doc.WriteString(fmt.Sprintf("%s %-30s [%s]\n", statusSymbol, title, statusText))
						
						artist := track.Artist
						if artist == "" {
							artist = "Unknown Artist"
						}
						doc.WriteString(fmt.Sprintf("  %s\n", greyStyle.Render(artist)))
					}
				}
			}

			if len(flatItems) > 6 {
				doc.WriteString("\n")
				indicator := fmt.Sprintf("Showing %d-%d of %d", start+1, end, len(flatItems))
				rightAligned := lipgloss.NewStyle().Width(99).Align(lipgloss.Right).Render(indicator)
				doc.WriteString(dimStyle.Render(rightAligned) + "\n")
			}
		}

		doc.WriteString("\n")
		doc.WriteString(dimStyle.Render("Press [up/down] to scroll | [esc] to return | [q] to quit"))
	}

	if m.Err != nil {
		doc.WriteString(fmt.Sprintf("\n\nError: %v", m.Err))
	}

	return borderStyle.Render(doc.String())
}
