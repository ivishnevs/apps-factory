//go:build !unix

package node

import "errors"

func freeBytes(string) (uint64, error) {
	return 0, errors.New("free disk space is not supported on this platform")
}
