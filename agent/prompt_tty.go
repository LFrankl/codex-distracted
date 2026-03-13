package agent

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// makeRawTTY puts f into raw mode and returns the previous state.
func makeRawTTY(f *os.File) (*term.State, error) {
	return term.MakeRaw(int(f.Fd()))
}

// restoreTTY restores f to its previous terminal state.
func restoreTTY(f *os.File, state *term.State) {
	term.Restore(int(f.Fd()), state)
}

// promptFallback is used when raw mode is unavailable (e.g. piped input).
// It accepts a number and returns the corresponding 0-based index.
func promptFallback(tty *os.File, title string, choices []Choice, defaultIdx int) int {
	fmt.Printf("%s\n", title)
	for i, c := range choices {
		marker := " "
		if i == defaultIdx {
			marker = "▸"
		}
		fmt.Printf("  %s %d. %s\n", marker, i+1, c.Label)
	}
	hint := ""
	if defaultIdx >= 0 {
		hint = fmt.Sprintf(" [%d]", defaultIdx+1)
	}
	fmt.Printf("Choice%s: ", hint)

	buf := make([]byte, 64)
	n, _ := tty.Read(buf)
	input := strings.TrimSpace(string(buf[:n]))

	if input == "" && defaultIdx >= 0 {
		return defaultIdx
	}
	if len(input) == 1 && input[0] >= '1' && int(input[0]-'0') <= len(choices) {
		return int(input[0]-'0') - 1
	}
	return -1
}
