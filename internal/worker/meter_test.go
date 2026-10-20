package worker

import (
	"reflect"
	"testing"

	"appfactory/internal/driver"
	"appfactory/internal/protocol"
)

// Usage lines are cumulative for the run: running usage from progress and the final usage of the
// result replace each other per model, never add up.
func TestMeterReplacesUsagePerModel(t *testing.T) {
	var m meter
	m.add(driver.Event{Usage: &protocol.Usage{Model: "opus", Input: 4, Output: 17, CacheRead: 22806, CacheWrite: 512}})
	m.add(driver.Event{Usage: &protocol.Usage{Model: "opus", Input: 8, Output: 60, CacheRead: 30000, CacheWrite: 600}})
	m.add(driver.Event{Usage: &protocol.Usage{Model: "opus", Input: 12, Output: 104, CacheRead: 37016, CacheWrite: 15102, CostUSD: 0.1377, CostSource: "claude_estimate"}})
	m.add(driver.Event{Usage: &protocol.Usage{Model: "haiku", Input: 412, Output: 18, CostUSD: 0.0029, CostSource: "claude_estimate"}})
	m.add(driver.Event{Cost: &driver.Cost{USD: 0.1406, Source: "claude_estimate"}})
	want := []protocol.Usage{
		{Model: "opus", Input: 12, Output: 104, CacheRead: 37016, CacheWrite: 15102, CostUSD: 0.1377, CostSource: "claude_estimate"},
		{Model: "haiku", Input: 412, Output: 18, CostUSD: 0.0029, CostSource: "claude_estimate"},
	}
	if got := m.usage(); !reflect.DeepEqual(got, want) {
		t.Fatalf("usage = %+v, want %+v", got, want)
	}
}
