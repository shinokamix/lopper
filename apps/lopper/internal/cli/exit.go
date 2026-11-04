package cli

import "errors"

// Exit statuses of lopper.
const (
	ExitOK      = 0
	ExitError   = 1
	ExitNotSafe = 2
)

// ExitCode returns the exit status for err, as returned by the root command:
// ExitNotSafe when every worktree rm left was not safe to delete, ExitError
// for any other error.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	if e, ok := errors.AsType[*notRemovedError](err); ok && e.failed == e.unsafe {
		return ExitNotSafe
	}
	return ExitError
}
