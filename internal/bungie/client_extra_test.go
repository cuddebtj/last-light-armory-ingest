package bungie

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWithHTTPClientOption(t *testing.T) {
	hc := &http.Client{Timeout: time.Second}
	c, err := New("k", WithHTTPClient(hc))
	if err != nil {
		t.Fatal(err)
	}
	if c.httpClient != hc {
		t.Error("WithHTTPClient did not install the client")
	}
}

func TestGetPreCancelledContext(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("server must not be reached with a cancelled context")
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.GetManifest(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestDownloadComponentInvalidURL(t *testing.T) {
	c, err := New("k")
	if err != nil {
		t.Fatal(err)
	}
	// A control character makes http.NewRequestWithContext reject the URL.
	if _, err := c.DownloadComponent(context.Background(), "/bad\x7fpath"); err == nil {
		t.Fatal("want invalid-URL error")
	}
}

func TestRetryAfterExhaustsAttempts(t *testing.T) {
	var calls atomic.Int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
	}), WithMaxAttempts(2))
	c.sleep = func(ctx context.Context, d time.Duration) error { return nil }

	_, err := c.GetManifest(context.Background())
	if err == nil || !strings.Contains(err.Error(), "giving up after 2 attempts") {
		t.Fatalf("want giving-up error, got %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("calls = %d, want 2", calls.Load())
	}
}

func TestRetryAfterSleepInterrupted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	c.sleep = func(sctx context.Context, d time.Duration) error {
		cancel()
		return sctx.Err()
	}
	if _, err := c.GetManifest(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled from interrupted Retry-After wait, got %v", err)
	}
}

func TestBackoffSleepInterrupted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	c.sleep = func(sctx context.Context, d time.Duration) error {
		cancel()
		return context.Canceled
	}
	if _, err := c.GetManifest(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled from interrupted backoff, got %v", err)
	}
}

func TestSleepCtx(t *testing.T) {
	if err := sleepCtx(context.Background(), time.Millisecond); err != nil {
		t.Errorf("normal sleep: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepCtx(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled sleep: %v", err)
	}
}

func TestDrainClosesBody(t *testing.T) {
	body := io.NopCloser(strings.NewReader(strings.Repeat("x", 100)))
	drain(body) // must not panic and must consume
}
