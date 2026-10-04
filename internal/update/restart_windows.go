package update

import (
	"os"
	"os/signal"
)

// Restart runs the binary at Exe, which after Install is the new release,
// with the same arguments. Windows cannot replace a process, so this one
// waits for the new one, leaves Ctrl+C to it and exits with its code. It
// returns only if it fails.
func (u *Updater) Restart() error {
	p, err := os.StartProcess(u.Exe, os.Args, &os.ProcAttr{Files: []*os.File{os.Stdin, os.Stdout, os.Stderr}}) //nolint:gosec // G702: Exe is this binary, as updated
	if err != nil {
		return err
	}
	signal.Ignore(os.Interrupt)
	state, err := p.Wait()
	if err != nil {
		return err
	}
	os.Exit(state.ExitCode())
	return nil
}
