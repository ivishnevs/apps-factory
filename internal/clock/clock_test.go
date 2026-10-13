package clock

import (
	"testing"
	"time"
)

func TestContinuityRestartsAfterGap(t *testing.T) {
	c := NewContinuity(3 * time.Second)
	t0 := time.Unix(1000, 0)
	if got := c.Observe(t0); !got.Equal(t0) {
		t.Fatal(got)
	}
	if got := c.Observe(t0.Add(3 * time.Second)); !got.Equal(t0) {
		t.Fatal("gap within threshold restarted window", got)
	}
	wake := t0.Add(2 * time.Hour)
	if got := c.Observe(wake); !got.Equal(wake) {
		t.Fatal("sleep did not restart window", got)
	}
	if got := c.Observe(wake.Add(-time.Minute)); !got.Equal(wake.Add(-time.Minute)) {
		t.Fatal("clock going backwards must restart window", got)
	}
}
