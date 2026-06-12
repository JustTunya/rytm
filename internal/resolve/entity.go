package resolve

// EntityType classifies a search result from YouTube Music.
type EntityType int

const (
	EntitySong EntityType = iota // Single track from YouTube Music catalog
	EntityAlbum                  // Full album / EP
	EntityVideo                  // Standard YouTube video (may be official MV)
	EntityPlaylist               // User-created or auto-generated playlist
)

func (e EntityType) String() string {
	switch e {
	case EntitySong:
		return "Song"
	case EntityAlbum:
		return "Album"
	case EntityVideo:
		return "Video"
	case EntityPlaylist:
		return "Playlist"
	default:
		return "Unknown"
	}
}

// Entity is the normalized representation of a single search result.
type Entity struct {
	Type         EntityType
	VideoID      string // YouTube video ID (for songs/videos)
	PlaylistID   string // YouTube playlist/album ID
	BrowseID     string // YouTube Music browse endpoint ID
	Title        string
	Artists      []string
	Album        string
	DurationSec  int    // Duration in seconds (0 if unknown)
	IsExplicit   bool
	IsOfficialMV bool   // Flagged as "Official Music Video"
	ThumbnailURL string
	Year         string
	TrackCount   int    // Number of tracks (for albums/playlists)
}

// URL returns the canonical YouTube URL for this entity.
func (e Entity) URL() string {
	if e.Type == EntityAlbum || e.Type == EntityPlaylist {
		// Prefer playlist ID for multi-track entities.
		if e.PlaylistID != "" {
			return "https://music.youtube.com/playlist?list=" + e.PlaylistID
		}
		// Fallback to browse endpoint.
		if e.BrowseID != "" {
			return "https://music.youtube.com/browse/" + e.BrowseID
		}
	}
	
	// Default to video watch URL.
	if e.VideoID != "" {
		return "https://music.youtube.com/watch?v=" + e.VideoID
	}
	return ""
}
