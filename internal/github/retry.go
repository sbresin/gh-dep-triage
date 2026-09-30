package github

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"time"
)

const maxRetryDelay = 60 * time.Second

type retryTransport struct {
	base       http.RoundTripper
	maxRetries int
	sleep      func(context.Context, time.Duration) error
}

func newRetryTransport(base http.RoundTripper) *retryTransport {
	return &retryTransport{base: base, maxRetries: 3, sleep: sleepCtx}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		resp, err := t.base.RoundTrip(req)
		if err != nil || attempt >= t.maxRetries || !isRateLimited(resp) {
			return resp, err
		}
		if req.Body != nil && req.GetBody == nil {
			return resp, nil
		}
		wait := retryDelay(resp, attempt)
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if err := t.sleep(req.Context(), wait); err != nil {
			return nil, err
		}
		if req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			req = req.Clone(req.Context())
			req.Body = body
		}
	}
}

func isRateLimited(resp *http.Response) bool {
	if resp.StatusCode == http.StatusTooManyRequests {
		return true
	}
	return resp.StatusCode == http.StatusForbidden &&
		(resp.Header.Get("Retry-After") != "" || resp.Header.Get("X-RateLimit-Remaining") == "0")
}

func retryDelay(resp *http.Response, attempt int) time.Duration {
	d := time.Second << attempt
	if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s >= 0 {
		d = time.Duration(s) * time.Second
	}
	return min(d, maxRetryDelay)
}
