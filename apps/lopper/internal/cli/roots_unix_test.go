//go:build unix

package cli

import (
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"
)

// A temporary directory variable may name anything, such as a FIFO,
// which blocks whoever opens it until something writes to it.
func TestTempRootsFIFO(t *testing.T) {
	home := realDir(t, t.TempDir())
	fifo := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}

	roots := make(chan []string, 1)
	go func() { roots <- withTemps([]string{home}, []string{fifo}) }()
	select {
	case got := <-roots:
		if want := []string{home}; !slices.Equal(got, want) {
			t.Errorf("withTemps() = %v, want %v", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("withTemps blocked on a FIFO")
	}
}
