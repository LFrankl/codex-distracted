package agent

import (
	"fmt"
	"os"
	"strings"
)

// Approver is called before a potentially dangerous action.
// Returns true to proceed, false to cancel.
type Approver func(kind, detail string) bool

// AutoApprover always approves without prompting (--auto-approve / -y flag).
func AutoApprover() Approver {
	return func(_, _ string) bool { return true }
}

// InteractiveApprover prompts the user with a numbered menu via /dev/tty.
func InteractiveApprover() Approver {
	return func(kind, detail string) bool {
		if detail != "" {
			fmt.Printf("\033[2m  %s\033[0m\n", detail)
		}
		idx := Prompt(kind, []Choice{
			{"Yes, proceed"},
			{"No, cancel"},
		}, 0)
		// 0 = Yes, anything else = No / cancelled
		return idx == 0
	}
}

// ttyReader opens /dev/tty for reading, falling back to stdin.
// Caller is responsible for closing the returned file if not os.Stdin.
func ttyReader() (*os.File, bool) {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return os.Stdin, false
	}
	return f, true
}

// readLine reads a line from /dev/tty (or stdin fallback).
func readLine() string {
	f, owned := ttyReader()
	if owned {
		defer f.Close()
	}
	buf := make([]byte, 256)
	n, _ := f.Read(buf)
	return strings.TrimSpace(string(buf[:n]))
}
