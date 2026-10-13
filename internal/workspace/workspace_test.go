package workspace

import (
	"context"
	"testing"
)

func TestOneWriter(t *testing.T) {
	a := Local{}
	path := t.TempDir()
	one, err := a.Acquire(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Acquire(context.Background(), path); err == nil {
		t.Fatal("second writer acquired workspace")
	}
	one.Release()
	two, err := a.Acquire(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	two.Release()
}
