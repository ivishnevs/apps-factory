//go:build !darwin && !linux

package node

import "errors"

func load1() (float64, error) { return 0, errors.New("load average is not supported on this platform") }
