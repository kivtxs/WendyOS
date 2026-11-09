//go:build !windows

package commands

import (
	"os"

	"golang.org/x/sys/unix"
)

// openPromptTTY opens the controlling terminal, which still reaches the person
// when both stdout and stderr are piped.
func openPromptTTY() (*os.File, func(), bool) {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, false
	}
	return f, func() { _ = f.Close() }, true
}

// stdinInForeground reports whether this process group owns the terminal on
// stdin; a background job does not.
func stdinInForeground() bool {
	pgrp, err := unix.IoctlGetInt(int(os.Stdin.Fd()), unix.TIOCGPGRP)
	return err == nil && pgrp == unix.Getpgrp()
}
