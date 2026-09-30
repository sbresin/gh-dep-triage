package github

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func newTestTransport(sleeps *[]time.Duration) *retryTransport {
	t := newRetryTransport(http.DefaultTransport)
	t.sleep = func(_ context.Context, d time.Duration) error { *sleeps = append(*sleeps, d); return nil }
	return t
}

func TestRetryHonoursRetryAfterAndReplaysBody(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		b, _ := io.ReadAll(r.Body)
		if string(b) != "payload" {
			t.Errorf("call %d body = %q", calls, b)
		}
		if calls < 3 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	var sleeps []time.Duration
	client := &http.Client{Transport: newTestTransport(&sleeps)}
	resp, err := client.Post(srv.URL, "text/plain", strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || calls != 3 {
		t.Fatalf("status %d after %d calls", resp.StatusCode, calls)
	}
	if diff := cmp.Diff([]time.Duration{2 * time.Second, 2 * time.Second}, sleeps); diff != "" {
		t.Errorf("sleeps (-want +got):\n%s", diff)
	}
}

func TestRetryGivesUpAfterThreeRetries(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	var sleeps []time.Duration
	client := &http.Client{Transport: newTestTransport(&sleeps)}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests || calls != 4 {
		t.Errorf("status %d after %d calls", resp.StatusCode, calls)
	}
	if diff := cmp.Diff([]time.Duration{time.Second, 2 * time.Second, 4 * time.Second}, sleeps); diff != "" {
		t.Errorf("backoff (-want +got):\n%s", diff)
	}
}

func TestPlainForbiddenIsNotRetried(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	var sleeps []time.Duration
	client := &http.Client{Transport: newTestTransport(&sleeps)}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if calls != 1 || len(sleeps) != 0 {
		t.Errorf("calls=%d sleeps=%v", calls, sleeps)
	}
}
