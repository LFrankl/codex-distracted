package agent

import (
	"fmt"
	"os"
	"strings"
)

// Choice is one numbered option in a menu prompt.
type Choice struct {
	Label string // displayed text
}

// Prompt renders a numbered menu and returns the index of the chosen option (0-based).
// Returns -1 if the user presses Enter with no input (selects default) or inputs 0/invalid.
// defaultIdx is the 0-based index highlighted as default (shown in bold), or -1 for none.
func Prompt(title string, choices []Choice, defaultIdx int) int {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		tty = os.Stdin
	} else {
		defer tty.Close()
	}

	fmt.Printf("\n\033[1;33m%s\033[0m\n", title)
	for i, c := range choices {
		n := i + 1
		if i == defaultIdx {
			fmt.Printf("  \033[1m%d. %s\033[0m\n", n, c.Label)
		} else {
			fmt.Printf("  %d. %s\n", n, c.Label)
		}
	}

	defaultHint := ""
	if defaultIdx >= 0 {
		defaultHint = fmt.Sprintf(" [%d]", defaultIdx+1)
	}
	fmt.Printf("\033[2mChoice%s: \033[0m", defaultHint)

	buf := make([]byte, 64)
	n, _ := tty.Read(buf)
	input := strings.TrimSpace(string(buf[:n]))

	if input == "" && defaultIdx >= 0 {
		return defaultIdx
	}

	// Parse single digit
	if len(input) == 1 && input[0] >= '1' && int(input[0]-'0') <= len(choices) {
		return int(input[0]-'0') - 1
	}

	return -1 // invalid / cancelled
}
