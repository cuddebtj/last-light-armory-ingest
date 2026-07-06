// Package bungie is the single wrapper around the Bungie.net Platform API
// and its definition CDN. Every HTTP call this project makes goes through
// Client, which owns the API key header, User-Agent, rate limiting, and
// retry/backoff behavior — no raw http.Get calls anywhere else.
//
// Scope guard: this client only touches public manifest data. It has no
// OAuth support on purpose; needing a bearer token here means the design has
// drifted from the ingest repo's scope (see CLAUDE.md "Non-Negotiables").
package bungie

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/time/rate"
)

// Default endpoints. Overridable via options for tests.
const (
	defaultPlatformURL = "https://www.bungie.net/Platform"
	defaultCDNURL      = "https://www.bungie.net"
)

// defaultUserAgent follows Bungie's suggested format:
// AppName/Version (+webUrl;contactEmail).
const defaultUserAgent = "LastLightArmoryIngest/1.0 (+https://github.com/cuddebtj/last-light-armory-ingest;cuddebtj@gmail.com)"

// Client talks to the Bungie.net Platform API and definition CDN.
// It is safe for concurrent use.
type Client struct {
	httpClient  *http.Client
	apiKey      string
	platformURL string
	cdnURL      string
	userAgent   string
	limiter     *rate.Limiter
	maxAttempts int
	// sleep is swapped out in tests so retry paths run instantly.
	sleep func(ctx context.Context, d time.Duration) error
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient replaces the underlying *http.Client (tests, custom
// transports).
func WithHTTPClient(hc *http.Client) Option { return func(c *Client) { c.httpClient = hc } }

// WithBaseURLs points the client at alternate Platform and CDN roots
// (httptest servers in unit tests).
func WithBaseURLs(platform, cdn string) Option {
	return func(c *Client) { c.platformURL, c.cdnURL = platform, cdn }
}

// WithUserAgent overrides the default User-Agent header.
func WithUserAgent(ua string) Option { return func(c *Client) { c.userAgent = ua } }

// WithRateLimit sets the request rate limit. Bungie publishes no hard
// number but does enforce limits; the default of 20 req/s with burst 5 is
// far below anything this job needs (it makes ~6 requests per run).
func WithRateLimit(rps float64, burst int) Option {
	return func(c *Client) { c.limiter = rate.NewLimiter(rate.Limit(rps), burst) }
}

// WithMaxAttempts sets the total number of tries per request (first attempt
// plus retries). Values below 1 are treated as 1.
func WithMaxAttempts(n int) Option {
	return func(c *Client) {
		if n < 1 {
			n = 1
		}
		c.maxAttempts = n
	}
}

// New builds a Client. apiKey must be a registered Bungie.net application
// key; it is sent as the X-API-Key header on every request.
func New(apiKey string, opts ...Option) (*Client, error) {
	if apiKey == "" {
		return nil, errors.New("bungie: API key is required")
	}
	c := &Client{
		httpClient:  &http.Client{Timeout: 5 * time.Minute}, // definition files are large
		apiKey:      apiKey,
		platformURL: defaultPlatformURL,
		cdnURL:      defaultCDNURL,
		userAgent:   defaultUserAgent,
		limiter:     rate.NewLimiter(rate.Limit(20), 5),
		maxAttempts: 4,
		sleep:       sleepCtx,
	}
	for _, o := range opts {
		o(c)
	}
	return c, nil
}

// GetManifest fetches GET /Destiny2/Manifest/ and returns the version string
// plus definition table paths.
func (c *Client) GetManifest(ctx context.Context) (*Manifest, error) {
	body, err := c.get(ctx, c.platformURL+"/Destiny2/Manifest/")
	if err != nil {
		return nil, fmt.Errorf("bungie: fetching manifest: %w", err)
	}
	defer body.Close()

	var envelope apiResponse[Manifest]
	if err := json.NewDecoder(body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("bungie: decoding manifest response: %w", err)
	}
	if envelope.ErrorCode != errorCodeSuccess {
		return nil, fmt.Errorf("bungie: manifest request failed: %s (code %d): %s",
			envelope.ErrorStatus, envelope.ErrorCode, envelope.Message)
	}
	if envelope.Response.Version == "" {
		return nil, errors.New("bungie: manifest response has empty version")
	}
	return &envelope.Response, nil
}

// DownloadComponent streams one definition table (a CDN path from
// Manifest.ComponentPath) as an io.ReadCloser. The caller must Close it.
// Definition files are large (DestinyInventoryItemDefinition is ~200 MB), so
// callers should decode them incrementally — see StreamDefinitions.
func (c *Client) DownloadComponent(ctx context.Context, path string) (io.ReadCloser, error) {
	if path == "" {
		return nil, errors.New("bungie: empty component path")
	}
	body, err := c.get(ctx, c.cdnURL+path)
	if err != nil {
		return nil, fmt.Errorf("bungie: downloading component %s: %w", path, err)
	}
	return body, nil
}

// get performs a rate-limited GET with retries. On success the response body
// is returned unread; on failure it is fully consumed and closed.
//
// Retried: HTTP 429 (honoring Retry-After), all 5xx, and transport errors.
// Not retried: other 4xx (the request itself is wrong; repeating it can't
// help) and context cancellation.
func (c *Client) get(ctx context.Context, url string) (io.ReadCloser, error) {
	var lastErr error
	for attempt := 1; attempt <= c.maxAttempts; attempt++ {
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-API-Key", c.apiKey)
		req.Header.Set("User-Agent", c.userAgent)

		resp, err := c.httpClient.Do(req)
		switch {
		case err != nil:
			lastErr = err // transport-level failure: retry
		case resp.StatusCode == http.StatusOK:
			return resp.Body, nil
		case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
			lastErr = fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
			if wait := retryAfter(resp); wait > 0 {
				drain(resp.Body)
				if attempt == c.maxAttempts {
					break
				}
				if err := c.sleep(ctx, wait); err != nil {
					return nil, err
				}
				continue
			}
			drain(resp.Body)
		default:
			drain(resp.Body)
			return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
		}

		if attempt == c.maxAttempts {
			break
		}
		if err := c.sleep(ctx, backoff(attempt)); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("giving up after %d attempts: %w", c.maxAttempts, lastErr)
}

// backoff returns the exponential backoff delay for the given 1-based
// attempt number, with full jitter: random in (0, base*2^(attempt-1)],
// capped at 30s. Jitter prevents synchronized retry stampedes.
func backoff(attempt int) time.Duration {
	base := 500 * time.Millisecond
	max := base << (attempt - 1) // 0.5s, 1s, 2s, 4s, ...
	if max > 30*time.Second {
		max = 30 * time.Second
	}
	return time.Duration(rand.Int64N(int64(max))) + time.Millisecond
}

// retryAfter parses a Retry-After header (delta-seconds form only, which is
// what Bungie sends). Returns 0 when absent or unparseable.
func retryAfter(resp *http.Response) time.Duration {
	v := resp.Header.Get("Retry-After")
	if v == "" {
		return 0
	}
	secs, err := strconv.Atoi(v)
	if err != nil || secs < 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}

// drain consumes and closes a response body so the underlying connection can
// be reused by the transport.
func drain(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 1<<20))
	body.Close()
}

// sleepCtx sleeps for d or until ctx is done, whichever comes first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
