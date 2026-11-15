package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"appfactory/internal/blobstore"
	"appfactory/internal/storage"
)

// meteredBlobs is a blob store whose volume usage the test sets.
type meteredBlobs struct {
	blobstore.Store
	usage blobstore.Usage
}

func (m *meteredBlobs) VolumeUsage() (blobstore.Usage, error) { return m.usage, nil }

// TestStorageAlert (run-data-retention): below 80 % nothing is said; from 80 % the company feed gets one attention
// item a day naming what frees space, however often the check runs.
func TestStorageAlert(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	m := &meteredBlobs{Store: h.s.Blobs, usage: blobstore.Usage{Total: 5000 << 20, Free: 2000 << 20}}
	h.s.Blobs = m
	alerts := func() []storage.FeedItem {
		t.Helper()
		page, err := h.s.Feed(ctx, storage.DefaultCompany, storage.FeedFilter{Attention: true}, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		var out []storage.FeedItem
		for _, f := range page.Items {
			if f.Type == "storage.low" {
				out = append(out, f)
			}
		}
		return out
	}
	if err := h.c.checkStorage(ctx); err != nil || len(alerts()) != 0 {
		t.Fatal("alert at 60 %", err)
	}
	m.usage.Free = 400 << 20
	h.fake.Advance(2 * time.Hour)
	for range 3 {
		if err := h.c.checkStorage(ctx); err != nil {
			t.Fatal(err)
		}
		h.fake.Advance(2 * time.Hour)
	}
	got := alerts()
	if len(got) != 1 || !strings.Contains(got[0].Summary, "92% full") || !strings.Contains(got[0].Summary, "FACTORY_RUN_RETENTION_DAYS") {
		t.Fatalf("%+v", got)
	}
	h.fake.Advance(24 * time.Hour)
	if err := h.c.checkStorage(ctx); err != nil || len(alerts()) != 2 {
		t.Fatal("no alert the next day", err)
	}
}
