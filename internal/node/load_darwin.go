package node

import (
	bin "encoding/binary"
	"errors"

	"golang.org/x/sys/unix"
)

// load1 reads vm.loadavg: struct loadavg {fixpt_t ldavg[3]; long fscale}.
func load1() (float64, error) {
	b, err := unix.SysctlRaw("vm.loadavg")
	if err != nil {
		return 0, err
	}
	if len(b) < 24 {
		return 0, errors.New("vm.loadavg: short value")
	}
	scale := bin.LittleEndian.Uint64(b[16:24])
	if scale == 0 {
		return 0, errors.New("vm.loadavg: zero scale")
	}
	return float64(bin.LittleEndian.Uint32(b[0:4])) / float64(scale), nil
}
