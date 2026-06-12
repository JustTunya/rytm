package resolve

import "errors"

var (
	ErrNoResults      = errors.New("resolve: no results found for query")
	ErrRateLimited    = errors.New("resolve: rate limited by upstream API")
	ErrTimeout        = errors.New("resolve: request timed out")
	ErrBadResponse    = errors.New("resolve: upstream returned malformed response")
	ErrProviderFailed = errors.New("resolve: provider returned a non-recoverable error")
)
