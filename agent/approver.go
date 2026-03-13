package agent

import (
	"fmt"
	"os"
	"strings"
)

// Approver is called before a potentially dangerous action.
// Returns (true, "") to proceed, (false, "") to cancel, or (false, instruction)
// when the user wants to redirect the agent with a custom instruction instead.
type Approver func(kind, detail string) (proceed bool, instruction string)

// AutoApprover always approves without prompting (--auto-approve / -y flag).
func AutoApprover() Approver {
	return func(_, _ string) (bool, string) { return true, "" }
}

// InteractiveApprover prompts the user with a numbered menu via /dev/tty.
// Option 3 lets the user type a custom instruction to redirect the agent.
func InteractiveApprover() Approver {
	return func(kind, detail string) (bool, string) {
		if detail != "" {
			fmt.Printf("\033[2m  %s\033[0m\n", detail)
		}
		idx := Prompt(kind, []Choice{
			{"Yes, proceed"},
			{"No, cancel"},
			{"Other instructions →"},
		}, 0)
		switch idx {
		case 0:
			return true, ""
		case 2:
			fmt.Printf("\033[2mInstruction: \033[0m")
			instr := readLine()
			if instr == "" {
				return false, ""
			}
			return false, instr
		default:
			return false, ""
		}
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
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	return strings.TrimSpace(string(buf[:n]))
}
