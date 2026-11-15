package blobstore

import "syscall"

// Usage is the size and free space of the volume a LocalFS keeps its blobs on.
type Usage struct {
	Total int64 `json:"total"`
	Free  int64 `json:"free"`
}

// UsedPercent is the share of the volume in use, 0–100.
func (u Usage) UsedPercent() int {
	if u.Total <= 0 {
		return 0
	}
	return int((u.Total - u.Free) * 100 / u.Total)
}

// VolumeUsage measures the volume under l.Root (storage-alerts: Core warns before a full volume refuses writes).
func (l LocalFS) VolumeUsage() (Usage, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(l.Root, &st); err != nil {
		return Usage{}, err
	}
	return Usage{Total: int64(st.Blocks) * int64(st.Bsize), Free: int64(st.Bavail) * int64(st.Bsize)}, nil
}
