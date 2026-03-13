package agent

import (
	"fmt"
	"os"
)

// Choice is one numbered option in a menu prompt.
type Choice struct {
	Label string
}

// Prompt renders an interactive menu navigable by arrow keys or number keys.
// Returns the 0-based index of the selected option, or defaultIdx on Enter.
// Returns -1 if the input cannot be parsed (should be treated as cancel).
func Prompt(title string, choices []Choice, defaultIdx int) int {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		tty = os.Stdin
	} else {
		defer tty.Close()
	}

	// Put tty into raw mode so we can read arrow keys without waiting for Enter
	oldState, err := makeRawTTY(tty)
	if err != nil {
		// Fallback: number input
		return promptFallback(tty, title, choices, defaultIdx)
	}
	defer restoreTTY(tty, oldState)

	selected := defaultIdx
	if selected < 0 {
		selected = 0
	}

	totalLines := len(choices) + 2 // title + choices + hint
	drawn := false

	render := func() {
		if drawn {
			// Move cursor back to the title line.
			// After the previous render, cursor is on the hint line (no trailing \r\n).
			// Hint line is (totalLines-1) lines below the title line.
			fmt.Printf("\033[%dA\r", totalLines-1)
		}
		drawn = true

		fmt.Printf("\r\033[K%s\r\n", title)
		for i, c := range choices {
			if i == selected {
				fmt.Printf("\r\033[K  \033[1;36m▸ %s\033[0m\r\n", c.Label)
			} else {
				fmt.Printf("\r\033[K    %s\r\n", c.Label)
			}
		}
		// Hint line — no trailing \r\n; cursor stays here until next render or clear.
		fmt.Printf("\r\033[K\033[2m  ↑↓ move  Enter confirm  1-%d jump\033[0m", len(choices))
	}

	render()

	buf := make([]byte, 8)
	for {
		n, _ := tty.Read(buf)
		if n == 0 {
			break
		}
		b := buf[:n]

		switch {
		case n == 1 && (b[0] == '\r' || b[0] == '\n'):
			clearMenu(totalLines)
			return selected

		case n == 1 && b[0] == 3: // Ctrl-C
			clearMenu(totalLines)
			return -1

		case n >= 3 && b[0] == '\033' && b[1] == '[' && b[2] == 'A': // ↑
			if selected > 0 {
				selected--
			} else {
				selected = len(choices) - 1
			}
			render()

		case n >= 3 && b[0] == '\033' && b[1] == '[' && b[2] == 'B': // ↓
			if selected < len(choices)-1 {
				selected++
			} else {
				selected = 0
			}
			render()

		case n == 1 && b[0] >= '1' && int(b[0]-'0') <= len(choices):
			selected = int(b[0]-'0') - 1
			render()
		}
	}
	clearMenu(totalLines)
	return selected
}

// clearMenu erases all menu lines in place.
// Cursor must be on the hint (last) line when called.
// After return, cursor is on the title line (first line of where the menu was).
func clearMenu(lines int) {
	// Cursor is on the last line (hint). Move to title line first.
	if lines > 1 {
		fmt.Printf("\033[%dA\r", lines-1)
	} else {
		fmt.Print("\r")
	}
	// Clear each line and advance.
	for i := 0; i < lines; i++ {
		fmt.Printf("\r\033[K\r\n")
	}
	// Move back up to the title line position.
	fmt.Printf("\033[%dA\r", lines)
}
