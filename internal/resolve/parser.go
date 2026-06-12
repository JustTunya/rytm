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

		musicShelfRenderer, ok := sectionMap["musicShelfRenderer"].(map[string]interface{})
		if !ok {
			continue
		}

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

			musicResponsiveListItemRenderer, ok := itemMap["musicResponsiveListItemRenderer"].(map[string]interface{})
			if !ok {
				continue
			}

			entity := parseMusicResponsiveListItemRenderer(musicResponsiveListItemRenderer, shelfTitle)
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
	var durationSec int
	var year string
	var itemType string
	isExplicit := false
	isOfficialMV := false

	metaRuns := extractAllRuns(flexColumns, 1)
	for i, run := range metaRuns {
		text, ok := run["text"].(string)
		if !ok {
			continue
		}

		navEndpoint, _ := run["navigationEndpoint"].(map[string]interface{})
		browseEndpoint, _ := navEndpoint["browseEndpoint"].(map[string]interface{})
		browseEndId, _ := browseEndpoint["browseId"].(string)

		// First metadata run is often the type ("Song", "Video", "Album", "Playlist") if it's a top-result shelf.
		if i == 0 && (text == "Song" || text == "Video" || text == "Album" || text == "Playlist") {
			itemType = text
			continue
		}

		if text == "Explicit" || text == "E" {
			isExplicit = true
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

		// Album check: if it has a browse ID starting with "MPREb" (release)
		if strings.HasPrefix(browseEndId, "MPREb") {
			album = text
			continue
		}

		// Duration check
		if isDurationStr(text) {
			durationSec = parseDuration(text)
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
		if len(artistNames) == 0 && browseEndId == "" && !isDurationStr(text) && text != "•" && text != "Video" {
			artistNames = append(artistNames, text)
		}
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

	return &Entity{
		Type:         eType,
		VideoID:      videoID,
		PlaylistID:   playlistID,
		BrowseID:     browseID,
		Title:        title,
		Artists:      artistNames,
		Album:        album,
		DurationSec:  durationSec,
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
	tvRenderer, ok := colMap["musicResponsiveListItemFlexColumnColumnRenderer"].(map[string]interface{})
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
	tvRenderer, ok := colMap["musicResponsiveListItemFlexColumnColumnRenderer"].(map[string]interface{})
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

func isDurationStr(s string) bool {
	return strings.Count(s, ":") == 1 || strings.Count(s, ":") == 2
}

func parseDuration(s string) int {
	parts := strings.Split(s, ":")
	total := 0
	mult := 1
	for i := len(parts) - 1; i >= 0; i-- {
		val, _ := strconv.Atoi(parts[i])
		total += val * mult
		mult *= 60
	}
	return total
}
