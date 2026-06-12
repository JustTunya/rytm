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
	// Wait on rate limiter
	if err := p.limiter.Wait(ctx); err != nil {
		return SearchResult{}, fmt.Errorf("rate limiter wait: %w", err)
	}

	payload, err := buildSearchPayload(query)
	if err != nil {
		return SearchResult{}, fmt.Errorf("build payload: %w", err)
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
			return SearchResult{}, ErrTimeout
		}
		// Return typed rate limit err if that's what failed
		if err.Error() == "rate limited (HTTP 429)" {
			return SearchResult{}, ErrRateLimited
		}
		return SearchResult{}, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusTooManyRequests {
			return SearchResult{}, ErrRateLimited
		}
		return SearchResult{}, fmt.Errorf("%w: HTTP %d - %s", ErrProviderFailed, resp.StatusCode, string(body))
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return SearchResult{}, fmt.Errorf("read body: %w", err)
	}

	entities, err := parseSearchResponse(bodyBytes)
	if err != nil {
		return SearchResult{}, fmt.Errorf("parse response: %w", err)
	}

	if len(entities) == 0 {
		return SearchResult{}, ErrNoResults
	}

	return SearchResult{
		Entities: entities,
		RawQuery: query,
	}, nil
}

func buildSearchPayload(query string) ([]byte, error) {
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
