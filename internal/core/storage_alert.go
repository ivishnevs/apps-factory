package core

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"appfactory/internal/blobstore"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
)

// Storage alerts (run-data-retention): a full blob volume refuses every upload and stops Core from starting, so Core
// measures it every storageCheckEvery and, from storageWarnPercent, logs an error for the operator and puts one
// attention item a day in every company's feed, naming what frees space.

const (
	storageCheckEvery  = time.Hour
	storageWarnPercent = 80
)

type volumeMeter interface {
	VolumeUsage() (blobstore.Usage, error)
}

func (c *Core) checkStorage(ctx context.Context) error {
	now := c.Store.Now()
	if !c.lastStorageCheck.IsZero() && now.Sub(c.lastStorageCheck) < storageCheckEvery {
		return nil
	}
	c.lastStorageCheck = now
	m, ok := c.Store.Blobs.(volumeMeter)
	if !ok {
		return nil
	}
	u, err := m.VolumeUsage()
	if err != nil {
		slog.Warn("blob volume usage", "error", err)
		return nil
	}
	if u.UsedPercent() < storageWarnPercent {
		return nil
	}
	msg := fmt.Sprintf("Factory storage is %d%% full (%d MiB free of %d MiB). Uploads fail when it is full. Shorten artifact retention (project artifact_retention_days), FACTORY_RUN_RETENTION_DAYS, or grow the volume.",
		u.UsedPercent(), u.Free>>20, u.Total>>20)
	slog.Error("blob volume nearly full", "used_percent", u.UsedPercent(), "free_mib", u.Free>>20)
	day := now.UTC().Format("2006-01-02")
	if c.lastStorageAlert == day {
		return nil
	}
	c.lastStorageAlert = day
	for _, company := range c.companies() {
		err := c.Store.AddFeed(ctx, company, storage.FeedItem{EventID: protocol.ID(), Type: "storage.low", EntityID: "storage", Actor: "system", Attention: true, Summary: msg})
		if c.isolate(ctx, company, "storage alert", err) != nil {
			return err
		}
	}
	return nil
}
