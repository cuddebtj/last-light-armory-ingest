package bungie

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newTestClient builds a Client aimed at the given handler, with instant
// sleeps so retry paths don't slow the suite down.
func newTestClient(t *testing.T, handler http.Handler, opts ...Option) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	base := []Option{
		WithBaseURLs(srv.URL, srv.URL),
		WithRateLimit(10000, 100),
	}
	c, err := New("test-key", append(base, opts...)...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.sleep = func(ctx context.Context, d time.Duration) error { return ctx.Err() }
	return c, srv
}

func TestNewRequiresAPIKey(t *testing.T) {
	if _, err := New(""); err == nil {
		t.Fatal("New(\"\") should fail")
	}
}

func TestGetManifestSendsHeaders(t *testing.T) {
	var gotKey, gotUA, gotPath string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-API-Key")
		gotUA = r.Header.Get("User-Agent")
		gotPath = r.URL.Path
		io.WriteString(w, `{"ErrorCode":1,"ErrorStatus":"Success","Response":{"version":"v1","jsonWorldComponentContentPaths":{"en":{"DestinyStatDefinition":"/common/stat.json"}}}}`)
	}), WithUserAgent("TestAgent/1.0"))

	m, err := c.GetManifest(context.Background())
	if err != nil {
		t.Fatalf("GetManifest: %v", err)
	}
	if gotKey != "test-key" {
		t.Errorf("X-API-Key = %q, want test-key", gotKey)
	}
	if gotUA != "TestAgent/1.0" {
		t.Errorf("User-Agent = %q, want TestAgent/1.0", gotUA)
	}
	if gotPath != "/Destiny2/Manifest/" {
		t.Errorf("path = %q, want /Destiny2/Manifest/", gotPath)
	}
	if m.Version != "v1" {
		t.Errorf("Version = %q, want v1", m.Version)
	}
	if p, ok := m.ComponentPath("en", "DestinyStatDefinition"); !ok || p != "/common/stat.json" {
		t.Errorf("ComponentPath = %q, %v", p, ok)
	}
}

func TestComponentPathMissing(t *testing.T) {
	m := &Manifest{JSONWorldComponentContentPaths: map[string]map[string]string{"en": {}}}
	if _, ok := m.ComponentPath("fr", "X"); ok {
		t.Error("missing locale should return ok=false")
	}
	if _, ok := m.ComponentPath("en", "X"); ok {
		t.Error("missing component should return ok=false")
	}
}

func TestGetManifestBungieErrorEnvelope(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"ErrorCode":2101,"ErrorStatus":"ApiInvalidOrExpiredKey","Message":"bad key","Response":{}}`)
	}))
	_, err := c.GetManifest(context.Background())
	if err == nil || !strings.Contains(err.Error(), "ApiInvalidOrExpiredKey") {
		t.Fatalf("want envelope error mentioning ApiInvalidOrExpiredKey, got %v", err)
	}
}

func TestGetManifestEmptyVersion(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"ErrorCode":1,"Response":{"version":""}}`)
	}))
	if _, err := c.GetManifest(context.Background()); err == nil {
		t.Fatal("empty version should error")
	}
}

func TestGetManifestMalformedJSON(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{not json`)
	}))
	if _, err := c.GetManifest(context.Background()); err == nil {
		t.Fatal("malformed JSON should error")
	}
}

func TestRetryOn500ThenSuccess(t *testing.T) {
	var calls atomic.Int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		io.WriteString(w, `{"ErrorCode":1,"Response":{"version":"v2"}}`)
	}))

	m, err := c.GetManifest(context.Background())
	if err != nil {
		t.Fatalf("GetManifest after retries: %v", err)
	}
	if m.Version != "v2" {
		t.Errorf("Version = %q, want v2", m.Version)
	}
	if calls.Load() != 3 {
		t.Errorf("server calls = %d, want 3", calls.Load())
	}
}

func TestRetryOn429HonorsRetryAfter(t *testing.T) {
	var calls atomic.Int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		io.WriteString(w, `{"ErrorCode":1,"Response":{"version":"v3"}}`)
	}))

	var slept []time.Duration
	c.sleep = func(ctx context.Context, d time.Duration) error {
		slept = append(slept, d)
		return nil
	}

	if _, err := c.GetManifest(context.Background()); err != nil {
		t.Fatalf("GetManifest: %v", err)
	}
	if len(slept) != 1 || slept[0] != time.Second {
		t.Errorf("slept %v, want exactly [1s] from Retry-After", slept)
	}
}

func TestGivesUpAfterMaxAttempts(t *testing.T) {
	var calls atomic.Int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}), WithMaxAttempts(2))

	_, err := c.GetManifest(context.Background())
	if err == nil || !strings.Contains(err.Error(), "giving up after 2 attempts") {
		t.Fatalf("want giving-up error, got %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("server calls = %d, want 2", calls.Load())
	}
}

func TestNoRetryOn404(t *testing.T) {
	var calls atomic.Int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))

	_, err := c.DownloadComponent(context.Background(), "/common/missing.json")
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("want 404 error, got %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("server calls = %d, want 1 (4xx must not retry)", calls.Load())
	}
}

func TestDownloadComponentStreamsBody(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/common/table.json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		io.WriteString(w, `{"1":{}}`)
	}))

	body, err := c.DownloadComponent(context.Background(), "/common/table.json")
	if err != nil {
		t.Fatalf("DownloadComponent: %v", err)
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	if string(data) != `{"1":{}}` {
		t.Errorf("body = %q", data)
	}
}

func TestDownloadComponentEmptyPath(t *testing.T) {
	c, err := New("k")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.DownloadComponent(context.Background(), ""); err == nil {
		t.Fatal("empty path should error")
	}
}

func TestContextCancellationStopsRetries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cancel() // cancel as soon as the first request lands
		w.WriteHeader(http.StatusInternalServerError)
	}))
	c.sleep = sleepCtx // real sleep: must be interrupted by cancellation

	_, err := c.GetManifest(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestWithMaxAttemptsFloorsAtOne(t *testing.T) {
	var calls atomic.Int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}), WithMaxAttempts(0))

	if _, err := c.GetManifest(context.Background()); err == nil {
		t.Fatal("want error")
	}
	if calls.Load() != 1 {
		t.Errorf("server calls = %d, want 1", calls.Load())
	}
}

func TestBackoffBoundsAndGrowth(t *testing.T) {
	for attempt := 1; attempt <= 10; attempt++ {
		max := 500 * time.Millisecond << (attempt - 1)
		if max > 30*time.Second {
			max = 30 * time.Second
		}
		for i := 0; i < 20; i++ {
			d := backoff(attempt)
			if d <= 0 || d > max+time.Millisecond {
				t.Fatalf("backoff(%d) = %v, want in (0, %v]", attempt, d, max+time.Millisecond)
			}
		}
	}
}

func TestRetryAfterParsing(t *testing.T) {
	mk := func(v string) *http.Response {
		h := http.Header{}
		if v != "" {
			h.Set("Retry-After", v)
		}
		return &http.Response{Header: h}
	}
	tests := []struct {
		header string
		want   time.Duration
	}{
		{"", 0},
		{"5", 5 * time.Second},
		{"0", 0},
		{"-3", 0},
		{"Wed, 21 Oct 2026 07:28:00 GMT", 0}, // HTTP-date form unsupported, treated as absent
		{"junk", 0},
	}
	for _, tt := range tests {
		if got := retryAfter(mk(tt.header)); got != tt.want {
			t.Errorf("retryAfter(%q) = %v, want %v", tt.header, got, tt.want)
		}
	}
}
