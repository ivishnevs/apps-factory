package web

import (
	"context"
	"fmt"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"appfactory/internal/storage"
	"appfactory/internal/tui"
)

// countingClient counts Core feed reads and refuses the limits Core refuses.
type countingClient struct {
	tui.Client
	reads int
}

func (c *countingClient) Feed(ctx context.Context, q tui.FeedQuery, after int64, limit int) (storage.Page[storage.FeedItem], error) {
	c.reads++
	if after < 0 || limit < 1 || limit > 100 {
		return storage.Page[storage.FeedItem]{}, fmt.Errorf("limit %d", limit)
	}
	return c.Client.Feed(ctx, q, after, limit)
}

func sparseFeed(n int) []storage.FeedItem {
	out := make([]storage.FeedItem, n)
	for i := range out {
		out[i] = storage.FeedItem{Sequence: int64(3*i + 2), Type: "task.updated", Attention: i%7 == 0, CreatedAt: time.Unix(int64(i), 0)}
	}
	return out
}

func tailServer(t *testing.T, feed []storage.FeedItem) (*httptest.Server, *countingClient) {
	t.Helper()
	c := &countingClient{Client: &tui.FixtureClient{F: tui.Fixture{Feed: feed}}}
	srv := httptest.NewServer((&Server{Client: c, Mode: "demo"}).Handler())
	t.Cleanup(srv.Close)
	return srv, c
}

// pageAll reads the whole feed backwards, page by page, and checks every page is oldest first.
func pageAll(t *testing.T, url string, limit int) ([]int64, int64) {
	t.Helper()
	var all []int64
	var top int64 = -1
	for before := int64(0); ; {
		var p storage.Page[storage.FeedItem]
		if code := getJSON(t, fmt.Sprintf("%s&before=%d&limit=%d", url, before, limit), &p); code != 200 {
			t.Fatal(code)
		}
		if top < 0 {
			top = p.NextAfter
		}
		if len(p.Items) > limit || (p.HasMore && len(p.Items) != limit) {
			t.Fatalf("page of %d (limit %d, more %v)", len(p.Items), limit, p.HasMore)
		}
		var seqs []int64
		for _, i := range p.Items {
			seqs = append(seqs, i.Sequence)
		}
		if !slices.IsSorted(seqs) {
			t.Fatal("page not oldest first", seqs)
		}
		all = append(seqs, all...)
		if !p.HasMore {
			return all, top
		}
		before = seqs[0]
	}
}

// TestFeedBefore (owner-web): ?before=0 answers the newest rows and the top sequence; paging back with
// before=<oldest shown> reads every row once, for the whole feed and for a filter.
func TestFeedBefore(t *testing.T) {
	feed := sparseFeed(1000)
	srv, c := tailServer(t, feed)
	var want, wantAttention []int64
	for _, i := range feed {
		want = append(want, i.Sequence)
		if i.Attention {
			wantAttention = append(wantAttention, i.Sequence)
		}
	}
	got, top := pageAll(t, srv.URL+"/api/feed?x=1", 100)
	if !slices.Equal(got, want) || top != feed[len(feed)-1].Sequence {
		t.Fatal(len(got), top)
	}
	if got, _ = pageAll(t, srv.URL+"/api/feed?attention=1", 30); !slices.Equal(got, wantAttention) {
		t.Fatal(got)
	}

	// The top is remembered: the next newest-first read costs one probe and one or two pages.
	c.reads = 0
	var p storage.Page[storage.FeedItem]
	getJSON(t, srv.URL+"/api/feed?before=0&limit=100", &p)
	if c.reads > 4 || len(p.Items) != 100 || p.Items[99].Sequence != top || !p.HasMore {
		t.Fatal(c.reads, len(p.Items), p.HasMore)
	}
}

// TestFeedBeforeSmall: an empty feed, a feed shorter than the limit and a bad before.
func TestFeedBeforeSmall(t *testing.T) {
	srv, _ := tailServer(t, nil)
	var p storage.Page[storage.FeedItem]
	if code := getJSON(t, srv.URL+"/api/feed?before=0", &p); code != 200 || len(p.Items) != 0 || p.HasMore || p.NextAfter != 0 {
		t.Fatal(code, p)
	}
	srv, _ = tailServer(t, sparseFeed(5))
	if getJSON(t, srv.URL+"/api/feed?before=0&limit=100", &p); len(p.Items) != 5 || p.HasMore || p.NextAfter != 14 {
		t.Fatal(p)
	}
	var bad map[string]string
	if code := getJSON(t, srv.URL+"/api/feed?before=-1", &bad); code != 400 {
		t.Fatal(code, bad)
	}
	// A filter matching nothing still answers the top, so a live stream starts there and not at the start.
	srv, _ = tailServer(t, []storage.FeedItem{{Sequence: 1}, {Sequence: 2}})
	if getJSON(t, srv.URL+"/api/feed?before=0&attention=1", &p); len(p.Items) != 0 || p.NextAfter != 2 {
		t.Fatal(p)
	}
}
