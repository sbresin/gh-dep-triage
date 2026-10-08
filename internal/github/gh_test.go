package github

import (
	"errors"
	"fmt"
	"testing"

	"github.com/cli/go-gh/v2/pkg/api"
)

func TestMapAuthError(t *testing.T) {
	unauthorized := &api.HTTPError{StatusCode: 401, Message: "Bad credentials"}
	if err := mapAuthError(fmt.Errorf("wrapped: %w", unauthorized)); !errors.Is(err, ErrNotAuthenticated) || !errors.Is(err, unauthorized) {
		t.Errorf("401: got %v, want ErrNotAuthenticated wrapping the HTTP error", err)
	}
	serverErr := &api.HTTPError{StatusCode: 500}
	if err := mapAuthError(serverErr); errors.Is(err, ErrNotAuthenticated) || err != error(serverErr) {
		t.Errorf("500: got %v, want unchanged", err)
	}
	if mapAuthError(nil) != nil {
		t.Error("nil must stay nil")
	}
}
