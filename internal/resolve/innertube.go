package resolve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"golang.org/x/time/rate"
)

const (
	innerTubeSearchURL = "https://music.youtube.com/youtubei/v1/search"
)

// InnerTubeProvider implements the Provider interface using the YouTube Music InnerTube API.
type InnerTubeProvider struct {
	client  *http.Client
	limiter *rate.Limiter // Token-bucket rate limiter
}

// NewInnerTubeProvider creates a new provider with a 2 req/s rate limit.
func NewInnerTubeProvider() *InnerTubeProvider {
	return &InnerTubeProvider{
		client: &http.Client{
			Timeout: 10 * time.Second, // Global request timeout
		},
		limiter: rate.NewLimiter(rate.Every(500*time.Millisecond), 1),
	}
}

func (p *InnerTubeProvider) Search(ctx context.Context, query string) (SearchResult, error) {
	// 1. General search to get mixed entities (songs, albums, playlists, videos)
	bodyBytes, err := p.searchWithPayload(ctx, query, "")
	if err != nil {
		return SearchResult{}, err
	}

	generalEntities, err := parseSearchResponse(bodyBytes)
	if err != nil {
		return SearchResult{}, fmt.Errorf("parse response: %w", err)
	}

	// 2. Songs-filtered search to get detailed song entities (with album names)
	// Param string "EgWKAQIIAWoKEAkQChADEAQQCg==" is for "Songs" filter on desktop WEB_REMIX
	songBodyBytes, err := p.searchWithPayload(ctx, query, "EgWKAQIIAWoKEAkQChADEAQQCg==")
	if err == nil {
		songEntities, err := parseSearchResponse(songBodyBytes)
		if err == nil {
			// Map VideoID to parsed Album name
			albumMap := make(map[string]string)
			for _, entity := range songEntities {
				if entity.Type == EntitySong && entity.VideoID != "" && entity.Album != "" {
					albumMap[entity.VideoID] = entity.Album
				}
			}

			// Update Album name on general search song entities
			for i := range generalEntities {
				if generalEntities[i].Type == EntitySong && generalEntities[i].VideoID != "" {
					if albumName, exists := albumMap[generalEntities[i].VideoID]; exists {
						generalEntities[i].Album = albumName
					}
				}
			}

			// Append any song from the filtered results that is not already present in general results
			existingVideoIDs := make(map[string]bool)
			for _, entity := range generalEntities {
				if entity.VideoID != "" {
					existingVideoIDs[entity.VideoID] = true
				}
			}
			for _, entity := range songEntities {
				if entity.VideoID != "" && !existingVideoIDs[entity.VideoID] {
					generalEntities = append(generalEntities, entity)
					existingVideoIDs[entity.VideoID] = true
				}
			}
		}
	}

	if len(generalEntities) == 0 {
		return SearchResult{}, ErrNoResults
	}

	return SearchResult{
		Entities: generalEntities,
		RawQuery: query,
	}, nil
}

func (p *InnerTubeProvider) searchWithPayload(ctx context.Context, query string, params string) ([]byte, error) {
	// Wait on rate limiter to space requests
	if err := p.limiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter wait: %w", err)
	}

	payload, err := buildSearchPayload(query, params)
	if err != nil {
		return nil, fmt.Errorf("build payload: %w", err)
	}

	// InnerTube Context Timeout
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	resp, err := p.doWithRetry(reqCtx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, innerTubeSearchURL, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		
		// Required headers for InnerTube API
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
		req.Header.Set("Origin", "https://music.youtube.com")
		req.Header.Set("Referer", "https://music.youtube.com/")
		
		return req, nil
	})

	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, ErrTimeout
		}
		if err.Error() == "rate limited (HTTP 429)" {
			return nil, ErrRateLimited
		}
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusTooManyRequests {
			return nil, ErrRateLimited
		}
		return nil, fmt.Errorf("%w: HTTP %d - %s", ErrProviderFailed, resp.StatusCode, string(body))
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	return bodyBytes, nil
}

func buildSearchPayload(query string, params string) ([]byte, error) {
	// Standard WEB_REMIX client context for YouTube Music InnerTube
	reqBody := map[string]interface{}{
		"context": map[string]interface{}{
			"client": map[string]interface{}{
				"clientName":    "WEB_REMIX",
				"clientVersion": "1.20240110.01.00",
				"hl":            "en",
				"gl":            "US",
			},
		},
		"query": query,
	}
	if params != "" {
		reqBody["params"] = params
	}
	return json.Marshal(reqBody)
}

func (p *InnerTubeProvider) doWithRetry(ctx context.Context, buildReq func() (*http.Request, error)) (*http.Response, error) {
	var lastErr error
	backoff := time.Second

	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
		}

		req, err := buildReq()
		if err != nil {
			return nil, err
		}

		resp, err := p.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		// Only retry on 429 or 503
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
			resp.Body.Close()
			lastErr = fmt.Errorf("rate limited (HTTP %d)", resp.StatusCode)
			continue
		}

		return resp, nil
	}

	return nil, fmt.Errorf("failed after 3 attempts: %w", lastErr)
}
