package github

import "context"

const searchPageSize = 100

type searchPage func(ctx context.Context, first int, after string) (rawSearch, error)

func searchAll(ctx context.Context, limit int, page searchPage) ([]SearchHit, error) {
	hits := []SearchHit{}
	after := ""
	for len(hits) < limit {
		res, err := page(ctx, min(searchPageSize, limit-len(hits)), after)
		if err != nil {
			return nil, err
		}
		hits = append(hits, res.hits()...)
		if !res.Search.PageInfo.HasNextPage || len(res.Search.Nodes) == 0 {
			break
		}
		after = res.Search.PageInfo.EndCursor
	}
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}
