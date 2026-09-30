package update

import (
	"os"
	"os/signal"
)

// Restart runs the binary at Exe in place of this process, with the same
// arguments: after Install, the release it installed. Windows cannot
// replace a process, so this one waits for it, leaving Ctrl+C to it, and
// exits as it does. It returns only if it fails.
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
