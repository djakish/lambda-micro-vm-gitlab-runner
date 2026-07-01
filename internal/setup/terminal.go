package setup

import "os"

// isTerminal reports whether f is attached to a character device (a TTY), used
// to decide whether to emit ANSI colors.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
