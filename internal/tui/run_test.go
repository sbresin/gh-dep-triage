package tui

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func TestRunCancelledContextExitsInterrupted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var code int
	var err error
	go func() {
		defer close(done)
		code, _, err = Run(ctx, fixture(), Deps{}, strings.NewReader(""), io.Discard)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop when its context was cancelled")
	}
	if code != 130 || err != nil {
		t.Errorf("code=%d err=%v, want 130 and nil", code, err)
	}
}
