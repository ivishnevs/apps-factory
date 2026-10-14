package node

import (
	"os"
	"strconv"
	"strings"
)

func load1() (float64, error) {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, err
	}
	f, _, _ := strings.Cut(string(b), " ")
	return strconv.ParseFloat(f, 64)
}
