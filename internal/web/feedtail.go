package web

import (
	"context"
	"sync"

	"appfactory/internal/storage"
	"appfactory/internal/tui"
)

// feedPageMax is Core's page size limit for GET /feed.
const feedPageMax = 100

// feedTail reads a feed backwards over Core's forward-only GET /feed: the newest limit rows (of the
// filter) with a sequence below before, or the newest rows of all when before is 0. Items are oldest
// first; HasMore says older rows exist; NextAfter is the newest sequence the read covered, where a live
// stream picks up.
//
// It reads windows of sequences ending at the top, doubling each window until it has limit+1 rows or
// reaches the start, so a dense feed costs one or two pages and a sparse filter a few more.
type feedTail struct {
	mu  sync.Mutex
	top int64 // the highest feed sequence seen so far (it only grows)
}

func (t *feedTail) read(ctx context.Context, c tui.Client, q tui.FeedQuery, before int64, limit int) (storage.Page[storage.FeedItem], error) {
	out := storage.Page[storage.FeedItem]{Items: []storage.FeedItem{}}
	hi := before - 1
	if before <= 0 {
		top, err := t.findTop(ctx, c)
		if err != nil {
			return out, err
		}
		hi = top
	}
	out.NextAfter = max(hi, 0)
	if hi < 1 {
		return out, nil
	}
	var items []storage.FeedItem
	span := int64(limit) + 1
	for lo := hi; ; span *= 2 {
		from := max(lo-span, 0)
		chunk, err := feedRange(ctx, c, q, from, lo)
		if err != nil {
			return out, err
		}
		items = append(chunk, items...)
		lo = from
		if len(items) > limit || from == 0 {
			break
		}
	}
	if len(items) > limit {
		items, out.HasMore = items[len(items)-limit:], true
	}
	out.Items = items
	return out, nil
}

// feedRange reads the rows with from < sequence <= to, oldest first.
func feedRange(ctx context.Context, c tui.Client, q tui.FeedQuery, from, to int64) ([]storage.FeedItem, error) {
	var out []storage.FeedItem
	for after := from; ; {
		page, err := c.Feed(ctx, q, after, feedPageMax)
		if err != nil {
			return nil, err
		}
		for _, item := range page.Items {
			if item.Sequence > to {
				return out, nil
			}
			out = append(out, item)
		}
		if !page.HasMore || page.NextAfter <= after {
			return out, nil
		}
		after = page.NextAfter
	}
}

// findTop is the feed's highest sequence: one probe when nothing was added since the last read, otherwise
// an exponential then binary search with one-row reads.
func (t *feedTail) findTop(ctx context.Context, c tui.Client) (int64, error) {
	t.mu.Lock()
	lo := t.top
	t.mu.Unlock()
	// probe answers the first sequence above x (0 when none) and whether another one follows it.
	probe := func(x int64) (int64, bool, error) {
		page, err := c.Feed(ctx, tui.FeedQuery{}, x, 1)
		if err != nil || len(page.Items) == 0 {
			return 0, false, err
		}
		return page.Items[0].Sequence, page.HasMore, nil
	}
	top, err := func() (int64, error) {
		first, more, err := probe(lo)
		if err != nil || first == 0 {
			return lo, err
		}
		if !more {
			return first, nil
		}
		// Invariant: a row exists above lo, and none above hi (once hi > 0).
		lo, hi := first, int64(0)
		for step := int64(1); hi == 0; step *= 2 {
			s, more, err := probe(lo + step)
			if err != nil {
				return 0, err
			}
			switch {
			case s == 0:
				hi = lo + step
			case !more:
				return s, nil
			default:
				lo = s
			}
		}
		for hi-lo > 1 {
			mid := lo + (hi-lo)/2
			s, more, err := probe(mid)
			if err != nil {
				return 0, err
			}
			switch {
			case s == 0:
				hi = mid
			case !more:
				return s, nil
			default:
				lo = s
			}
		}
		return hi, nil
	}()
	if err != nil {
		return 0, err
	}
	t.mu.Lock()
	t.top = max(t.top, top)
	top = t.top
	t.mu.Unlock()
	return top, nil
}
