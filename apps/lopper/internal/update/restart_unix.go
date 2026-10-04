//go:build !windows

package update

import (
	"os"
	"syscall"
)

// Restart replaces this process with the binary at Exe, which after Install
// is the new release, and passes the same arguments. It returns only if it
// fails.
func (u *Updater) Restart() error {
	return syscall.Exec(u.Exe, os.Args, os.Environ()) //nolint:gosec // G204: Exe is this binary, as updated
}
