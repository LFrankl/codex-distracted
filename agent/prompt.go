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

	render := func() {
		// Move cursor up by number of printed lines then redraw
		fmt.Printf("\r\033[K%s\r\n", title)
		for i, c := range choices {
			if i == selected {
				fmt.Printf("\r  \033[1;36m▸ %s\033[0m\r\n", c.Label)
			} else {
				fmt.Printf("\r    %s\r\n", c.Label)
			}
		}
		fmt.Printf("\033[2m  ↑↓ move  Enter confirm  1-%d jump\033[0m", len(choices))
		// Move cursor back to top of menu so next render overwrites cleanly
		// Lines printed: 1 (title) + len(choices) + 1 (hint)
		lines := len(choices) + 2
		fmt.Printf("\033[%dA", lines)
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
			// Clear the menu before returning
			clearMenu(len(choices) + 2)
			return selected

		case n == 1 && b[0] == 3: // Ctrl-C
			clearMenu(len(choices) + 2)
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
	clearMenu(len(choices) + 2)
	return selected
}

// clearMenu moves down past the menu and erases it.
func clearMenu(lines int) {
	// Move down to below the menu, then erase each line going up
	fmt.Printf("\033[%dB", lines)
	for i := 0; i < lines; i++ {
		fmt.Printf("\r\033[K\033[1A")
	}
	fmt.Printf("\r\033[K")
}
