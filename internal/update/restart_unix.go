//go:build !windows

package update

import (
	"os"
	"syscall"
)

// Restart runs the binary at Exe in place of this process, with the same
// arguments: after Install, the release it installed. It returns only if
// it fails.
func (u *Updater) Restart() error {
	return syscall.Exec(u.Exe, os.Args, os.Environ()) //nolint:gosec // G204: Exe is this binary, as updated
}
