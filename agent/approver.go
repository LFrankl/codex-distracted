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

// InteractiveApprover prompts the user via /dev/tty, bypassing any readline
// buffering that may be active in REPL mode.
func InteractiveApprover() Approver {
	return func(kind, detail string) bool {
		fmt.Printf("\033[1;33m? %s\033[0m [y/N] ", kind)

		tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if err != nil {
			// Fallback to stdin if /dev/tty unavailable
			tty = os.Stdin
		} else {
			defer tty.Close()
		}

		var input string
		buf := make([]byte, 64)
		n, _ := tty.Read(buf)
		input = strings.TrimSpace(strings.ToLower(string(buf[:n])))

		return input == "y" || input == "yes"
	}
}
