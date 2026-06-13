package resolve

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func parseSearchResponse(data []byte) ([]Entity, error) {
	var root map[string]interface{}
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("unmarshal root: %w", err)
	}

	contents, ok := root["contents"].(map[string]interface{})
	if !ok {
		return nil, ErrBadResponse
	}

	tabbedSearchResultsRenderer, ok := contents["tabbedSearchResultsRenderer"].(map[string]interface{})
	if !ok {
		return nil, ErrBadResponse
	}

	tabs, ok := tabbedSearchResultsRenderer["tabs"].([]interface{})
	if !ok || len(tabs) == 0 {
		return nil, ErrBadResponse
	}

	tab, ok := tabs[0].(map[string]interface{})
	if !ok {
		return nil, ErrBadResponse
	}

	tabRenderer, ok := tab["tabRenderer"].(map[string]interface{})
	if !ok {
		return nil, ErrBadResponse
	}

	content, ok := tabRenderer["content"].(map[string]interface{})
	if !ok {
		return nil, ErrBadResponse
	}

	sectionListRenderer, ok := content["sectionListRenderer"].(map[string]interface{})
	if !ok {
		return nil, ErrBadResponse
	}

	sections, ok := sectionListRenderer["contents"].([]interface{})
	if !ok {
		return nil, ErrBadResponse
	}

	var entities []Entity

	for _, sectionItem := range sections {
		sectionMap, ok := sectionItem.(map[string]interface{})
		if !ok {
			continue
		}

		// Normal search results
		if musicShelfRenderer, ok := sectionMap["musicShelfRenderer"].(map[string]interface{}); ok {
			shelfTitle := extractShelfTitle(musicShelfRenderer)
			contentsList, ok := musicShelfRenderer["contents"].([]interface{})
			if !ok {
				continue
			}

			for _, item := range contentsList {
				itemMap, ok := item.(map[string]interface{})
				if !ok {
					continue
				}

				if mrlir, ok := itemMap["musicResponsiveListItemRenderer"].(map[string]interface{}); ok {
					entity := parseMusicResponsiveListItemRenderer(mrlir, shelfTitle)
					if entity != nil {
						entities = append(entities, *entity)
					}
				}
			}
		}

		// Exact matches often return itemSectionRenderer with a single musicResponsiveListItemRenderer inside
		if itemSectionRenderer, ok := sectionMap["itemSectionRenderer"].(map[string]interface{}); ok {
			contentsList, ok := itemSectionRenderer["contents"].([]interface{})
			if !ok {
				continue
			}

			for _, item := range contentsList {
				itemMap, ok := item.(map[string]interface{})
				if !ok {
					continue
				}

				if mrlir, ok := itemMap["musicResponsiveListItemRenderer"].(map[string]interface{}); ok {
					entity := parseMusicResponsiveListItemRenderer(mrlir, "") // Use empty string to rely on itemType
					if entity != nil {
						entities = append(entities, *entity)
					}
				}
			}
		}

		// "Top result" card for exact matches
		if musicCardShelfRenderer, ok := sectionMap["musicCardShelfRenderer"].(map[string]interface{}); ok {
			entity := parseMusicCardShelfRenderer(musicCardShelfRenderer)
			if entity != nil {
				entities = append(entities, *entity)
			}
		}
	}

	return entities, nil
}

func extractShelfTitle(musicShelfRenderer map[string]interface{}) string {
	titleObj, ok := musicShelfRenderer["title"].(map[string]interface{})
	if !ok {
		return ""
	}
	runs, ok := titleObj["runs"].([]interface{})
	if !ok || len(runs) == 0 {
		return ""
	}
	runMap, ok := runs[0].(map[string]interface{})
	if !ok {
		return ""
	}
	text, _ := runMap["text"].(string)
	return strings.ToLower(strings.TrimSpace(text))
}

func parseMusicResponsiveListItemRenderer(renderer map[string]interface{}, shelfTitle string) *Entity {
	flexColumns, ok := renderer["flexColumns"].([]interface{})
	if !ok || len(flexColumns) == 0 {
		return nil
	}

	// Column 1 is usually the Title
	titleRun := extractFirstRun(flexColumns, 0)
	if titleRun == nil {
		return nil
	}
	title, _ := titleRun["text"].(string)

	videoID := extractVideoID(titleRun)
	browseID, playlistID := extractBrowseAndPlaylistID(renderer)

	// Column 2 contains metadata runs (Artist, Album, Duration, Views, etc.)
	var artistNames []string
	var album string
	var year string
	var itemType string
	isExplicit := false
	isOfficialMV := false

	for colIdx := 1; colIdx < len(flexColumns); colIdx++ {
		metaRuns := extractAllRuns(flexColumns, colIdx)
		for i, run := range metaRuns {
			text, ok := run["text"].(string)
			if !ok {
				continue
			}

			navEndpoint, _ := run["navigationEndpoint"].(map[string]interface{})
			browseEndpoint, _ := navEndpoint["browseEndpoint"].(map[string]interface{})
			browseEndId, _ := browseEndpoint["browseId"].(string)

			// First metadata run is often the type ("Song", "Video", "Album", "EP", "Single", "Playlist", "Artist", etc.) if it's a top-result shelf.
			if colIdx == 1 && i == 0 && (text == "Song" || text == "Video" || text == "Album" || text == "EP" || text == "Single" || text == "Playlist" || text == "Artist" || text == "Profile" || text == "Episode" || text == "Podcast") {
				if text == "EP" || text == "Single" {
					itemType = "Album" // Treat EPs and Singles as Albums
				} else {
					itemType = text
				}
				continue
			}

			if text == "Explicit" || text == "E" || strings.TrimSpace(text) == "•" {
				if text == "Explicit" || text == "E" {
					isExplicit = true
				}
				continue
			}

			if strings.Contains(text, "views") {
				continue // skip view counts
			}

			// Artist check: if it has a browse ID starting with "UC" (channel)
			if strings.HasPrefix(browseEndId, "UC") {
				artistNames = append(artistNames, text)
				continue
			}

			// Album check: if it has a browse ID starting with "MPREb" or "FEmusic_release_detail"
			if strings.HasPrefix(browseEndId, "MPREb") || strings.HasPrefix(browseEndId, "FEmusic_release_detail") {
				album = text
				continue
			}

			// Year check (4 digits)
			if len(text) == 4 {
				if _, err := strconv.Atoi(text); err == nil {
					year = text
					continue
				}
			}
			
			// If we are in "videos" shelf, the first run without browse id is often artist
			if colIdx == 1 && len(artistNames) == 0 && browseEndId == "" && !isDurationStr(text) && text != "•" && text != "Video" {
				artistNames = append(artistNames, text)
			}
		}
	}

	// Reject non-audio/video entities
	if itemType == "Artist" || itemType == "Profile" || itemType == "Episode" || itemType == "Podcast" {
		return nil
	}

	// Detect type based on shelf or itemType
	var eType EntityType
	if shelfTitle == "songs" || itemType == "Song" {
		eType = EntitySong
	} else if shelfTitle == "albums" || itemType == "Album" {
		eType = EntityAlbum
	} else if shelfTitle == "community playlists" || itemType == "Playlist" {
		eType = EntityPlaylist
	} else if shelfTitle == "videos" || itemType == "Video" {
		eType = EntityVideo
	} else {
		// Fallback
		if videoID != "" {
			eType = EntityVideo
		} else {
			eType = EntityPlaylist
		}
	}

	// Official MV check
	if eType == EntityVideo && strings.Contains(strings.ToLower(title), "official music video") {
		isOfficialMV = true
	}

	// Thumbnails
	var thumbUrl string
	if thumbnailObj, ok := renderer["thumbnail"].(map[string]interface{}); ok {
		if musicThumbnail, ok := thumbnailObj["musicThumbnailRenderer"].(map[string]interface{}); ok {
			if th, ok := musicThumbnail["thumbnail"].(map[string]interface{}); ok {
				if thArr, ok := th["thumbnails"].([]interface{}); ok && len(thArr) > 0 {
					if firstTh, ok := thArr[0].(map[string]interface{}); ok {
						thumbUrl, _ = firstTh["url"].(string)
					}
				}
			}
		}
	}

	if eType == EntityAlbum && album == "" {
		album = title
	}

	return &Entity{
		Type:         eType,
		VideoID:      videoID,
		PlaylistID:   playlistID,
		BrowseID:     browseID,
		Title:        title,
		Artists:      artistNames,
		Album:        album,
		IsExplicit:   isExplicit,
		IsOfficialMV: isOfficialMV,
		ThumbnailURL: thumbUrl,
		Year:         year,
	}
}

func extractFirstRun(flexColumns []interface{}, colIdx int) map[string]interface{} {
	if colIdx >= len(flexColumns) {
		return nil
	}
	colMap, ok := flexColumns[colIdx].(map[string]interface{})
	if !ok {
		return nil
	}
	tvRenderer, ok := colMap["musicResponsiveListItemFlexColumnRenderer"].(map[string]interface{})
	if !ok {
		return nil
	}
	textObj, ok := tvRenderer["text"].(map[string]interface{})
	if !ok {
		return nil
	}
	runs, ok := textObj["runs"].([]interface{})
	if !ok || len(runs) == 0 {
		return nil
	}
	runMap, ok := runs[0].(map[string]interface{})
	if !ok {
		return nil
	}
	return runMap
}

func extractAllRuns(flexColumns []interface{}, colIdx int) []map[string]interface{} {
	if colIdx >= len(flexColumns) {
		return nil
	}
	colMap, ok := flexColumns[colIdx].(map[string]interface{})
	if !ok {
		return nil
	}
	tvRenderer, ok := colMap["musicResponsiveListItemFlexColumnRenderer"].(map[string]interface{})
	if !ok {
		return nil
	}
	textObj, ok := tvRenderer["text"].(map[string]interface{})
	if !ok {
		return nil
	}
	runs, ok := textObj["runs"].([]interface{})
	if !ok {
		return nil
	}

	var out []map[string]interface{}
	for _, run := range runs {
		if runMap, ok := run.(map[string]interface{}); ok {
			out = append(out, runMap)
		}
	}
	return out
}

func extractVideoID(run map[string]interface{}) string {
	navEndpoint, ok := run["navigationEndpoint"].(map[string]interface{})
	if !ok {
		return ""
	}
	watchEndpoint, ok := navEndpoint["watchEndpoint"].(map[string]interface{})
	if !ok {
		return ""
	}
	vId, _ := watchEndpoint["videoId"].(string)
	return vId
}

func extractBrowseAndPlaylistID(renderer map[string]interface{}) (browseID, playlistID string) {
	navEndpoint, ok := renderer["navigationEndpoint"].(map[string]interface{})
	if !ok {
		return "", ""
	}
	
	if browseEndpoint, ok := navEndpoint["browseEndpoint"].(map[string]interface{}); ok {
		browseID, _ = browseEndpoint["browseId"].(string)
	}
	if watchEndpoint, ok := navEndpoint["watchEndpoint"].(map[string]interface{}); ok {
		playlistID, _ = watchEndpoint["playlistId"].(string)
	}
	return
}


func parseMusicCardShelfRenderer(renderer map[string]interface{}) *Entity {
	titleObj, ok := renderer["title"].(map[string]interface{})
	if !ok {
		return nil
	}
	runs, ok := titleObj["runs"].([]interface{})
	if !ok || len(runs) == 0 {
		return nil
	}
	titleRun, ok := runs[0].(map[string]interface{})
	if !ok {
		return nil
	}
	title, _ := titleRun["text"].(string)

	var videoID string
	var itemType string
	var artistNames []string
	var album string
	var year string

	if buttons, ok := renderer["buttons"].([]interface{}); ok && len(buttons) > 0 {
		if bMap, ok := buttons[0].(map[string]interface{}); ok {
			if bRenderer, ok := bMap["buttonRenderer"].(map[string]interface{}); ok {
				if cmd, ok := bRenderer["command"].(map[string]interface{}); ok {
					if we, ok := cmd["watchEndpoint"].(map[string]interface{}); ok {
						videoID, _ = we["videoId"].(string)
					}
				}
			}
		}
	}

	if subtitle, ok := renderer["subtitle"].(map[string]interface{}); ok {
		if sRuns, ok := subtitle["runs"].([]interface{}); ok {
			for i, r := range sRuns {
				rMap, ok := r.(map[string]interface{})
				if !ok {
					continue
				}
				text, _ := rMap["text"].(string)

				if i == 0 && (text == "Song" || text == "Video" || text == "Album" || text == "Playlist") {
					itemType = text
					continue
				}

				if text == " • " || text == "Explicit" || text == "E" {
					continue
				}

				if strings.Contains(text, "views") {
					continue
				}


				if len(text) == 4 {
					if _, err := strconv.Atoi(text); err == nil {
						year = text
						continue
					}
				}

				navEndpoint, _ := rMap["navigationEndpoint"].(map[string]interface{})
				browseEndpoint, _ := navEndpoint["browseEndpoint"].(map[string]interface{})
				browseEndId, _ := browseEndpoint["browseId"].(string)

				if strings.HasPrefix(browseEndId, "UC") {
					artistNames = append(artistNames, text)
					continue
				}

				if strings.HasPrefix(browseEndId, "MPREb") || strings.HasPrefix(browseEndId, "FEmusic_release_detail") {
					album = text
					continue
				}

				if len(artistNames) == 0 && browseEndId == "" && !isDurationStr(text) {
					artistNames = append(artistNames, text)
				}
			}
		}
	}

	var eType EntityType
	if itemType == "Song" {
		eType = EntitySong
	} else if itemType == "Album" {
		eType = EntityAlbum
	} else if itemType == "Playlist" {
		eType = EntityPlaylist
	} else if itemType == "Video" {
		eType = EntityVideo
	} else {
		if videoID != "" {
			eType = EntityVideo
		} else {
			eType = EntityPlaylist
		}
	}

	var thumbUrl string
	if thumbnailObj, ok := renderer["thumbnail"].(map[string]interface{}); ok {
		if musicThumbnail, ok := thumbnailObj["musicThumbnailRenderer"].(map[string]interface{}); ok {
			if th, ok := musicThumbnail["thumbnail"].(map[string]interface{}); ok {
				if thArr, ok := th["thumbnails"].([]interface{}); ok && len(thArr) > 0 {
					if firstTh, ok := thArr[0].(map[string]interface{}); ok {
						thumbUrl, _ = firstTh["url"].(string)
					}
				}
			}
		}
	}

	if eType == EntityAlbum && album == "" {
		album = title
	}

	return &Entity{
		Type:         eType,
		VideoID:      videoID,
		Title:        title,
		Artists:      artistNames,
		Album:        album,
		Year:         year,
		ThumbnailURL: thumbUrl,
	}
}

func isDurationStr(s string) bool {
	return strings.Count(s, ":") == 1 || strings.Count(s, ":") == 2
}
