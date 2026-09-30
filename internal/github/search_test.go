package github

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// fakePages serves `total` hits in pages of the requested size.
func fakePages(total int, firsts *[]int) searchPage {
	served := 0
	return func(_ context.Context, first int, after string) (rawSearch, error) {
		*firsts = append(*firsts, first)
		if (served == 0) != (after == "") {
			return rawSearch{}, fmt.Errorf("unexpected cursor %q at %d", after, served)
		}
		var r rawSearch
		for i := 0; i < first && served < total; i++ {
			served++
			r.Search.Nodes = append(r.Search.Nodes, struct {
				Number     int
				Title      string
				URL        string
				Repository struct{ NameWithOwner string }
				Author     *rawActor
			}{Number: served})
		}
		r.Search.PageInfo.HasNextPage = served < total
		r.Search.PageInfo.EndCursor = fmt.Sprint(served)
		return r, nil
	}
}

func TestSearchAllPaginatesToLimit(t *testing.T) {
	var firsts []int
	hits, err := searchAll(context.Background(), 250, fakePages(300, &firsts))
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 250 {
		t.Errorf("got %d hits", len(hits))
	}
	if diff := cmp.Diff([]int{100, 100, 50}, firsts); diff != "" {
		t.Errorf("page sizes (-want +got):\n%s", diff)
	}
}

func TestSearchAllStopsWhenExhausted(t *testing.T) {
	var firsts []int
	hits, err := searchAll(context.Background(), 200, fakePages(30, &firsts))
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 30 || len(firsts) != 1 {
		t.Errorf("hits=%d pages=%d", len(hits), len(firsts))
	}
}
