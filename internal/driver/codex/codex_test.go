package codex

import (
	"slices"
	"testing"

	"appfactory/internal/config"
	"appfactory/internal/driver"
	"appfactory/internal/node"
	"appfactory/internal/protocol"
)

var (
	_ node.DriverProbe = Driver{}
	_ driver.Driver    = Driver{}
)

func TestOptionsRegistered(t *testing.T) {
	if !slices.Equal(config.RuntimeOptions[protocol.RuntimeCodex], Options) {
		t.Fatalf("config.RuntimeOptions[codex] = %v, driver reads %v", config.RuntimeOptions[protocol.RuntimeCodex], Options)
	}
}
