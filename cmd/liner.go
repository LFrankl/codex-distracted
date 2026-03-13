package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"codex/config"
)

// liner is a minimal line editor that correctly handles multi-byte (CJK) input.
// It replaces chzyer/readline to fix the double-echo bug on long/CJK lines.
type liner struct {
	historyFile string
	history     []string
	histPos     int // current position when browsing (-1 = not browsing)
	buf         []rune
	cursor      int
	prompt      string
}

func newLiner(historyFile string) *liner {
	l := &liner{historyFile: historyFile, histPos: -1}
	l.loadHistory()
	return l
}

func (l *liner) SetPrompt(p string) { l.prompt = p }

func (l *liner) printableLen(s string) int {
	// Strip ANSI escape sequences to get the visual length
	n := 0
	inEsc := false
	for _, r := range s {
		if inEsc {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inEsc = false
			}
			continue
		}
		if r == '\033' {
			inEsc = true
			continue
		}
		// CJK and other wide characters count as 2 columns
		if r > 0x2E7F {
			n += 2
		} else {
			n++
		}
	}
	return n
}

func (l *liner) Readline() (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		// Non-interactive (pipe/redirect): plain bufio read
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			return scanner.Text(), nil
		}
		return "", fmt.Errorf("EOF")
	}

	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return l.fallbackReadline()
	}
	defer term.Restore(fd, oldState)

	l.buf = l.buf[:0]
	l.cursor = 0
	l.histPos = -1

	l.redraw("")

	for {
		b := make([]byte, 8)
		n, err := os.Stdin.Read(b)
		if err != nil || n == 0 {
			fmt.Print("\r\n")
			return "", fmt.Errorf("EOF")
		}
		seq := b[:n]

		switch {
		case n == 1 && seq[0] == '\r' || seq[0] == '\n':
			// Enter
			line := string(l.buf)
			fmt.Print("\r\n")
			if line != "" {
				l.addHistory(line)
			}
			return line, nil

		case n == 1 && (seq[0] == 3): // Ctrl-C
			fmt.Print("^C\r\n")
			l.buf = l.buf[:0]
			l.cursor = 0
			l.redraw("")
			continue

		case n == 1 && seq[0] == 4: // Ctrl-D
			fmt.Print("\r\n")
			return "", fmt.Errorf("EOF")

		case n == 1 && seq[0] == 127 || seq[0] == 8: // Backspace
			if l.cursor > 0 {
				l.buf = append(l.buf[:l.cursor-1], l.buf[l.cursor:]...)
				l.cursor--
				l.redraw("")
			}

		case n == 1 && seq[0] == 21: // Ctrl-U: clear line
			l.buf = l.buf[:0]
			l.cursor = 0
			l.redraw("")

		case n == 1 && seq[0] == 1: // Ctrl-A: start of line
			l.cursor = 0
			l.redraw("")

		case n == 1 && seq[0] == 5: // Ctrl-E: end of line
			l.cursor = len(l.buf)
			l.redraw("")

		case n >= 3 && seq[0] == '\033' && seq[1] == '[':
			switch seq[2] {
			case 'A': // Up arrow: history prev
				l.historyPrev()
			case 'B': // Down arrow: history next
				l.historyNext()
			case 'C': // Right arrow
				if l.cursor < len(l.buf) {
					l.cursor++
					l.redraw("")
				}
			case 'D': // Left arrow
				if l.cursor > 0 {
					l.cursor--
					l.redraw("")
				}
			case '3': // Delete key (ESC[3~)
				if n >= 4 && seq[3] == '~' && l.cursor < len(l.buf) {
					l.buf = append(l.buf[:l.cursor], l.buf[l.cursor+1:]...)
					l.redraw("")
				}
			}

		default:
			// Regular character(s) — decode as UTF-8 runes
			runes := []rune(string(seq[:n]))
			// Filter out non-printable bytes
			var printable []rune
			for _, r := range runes {
				if r >= 32 {
					printable = append(printable, r)
				}
			}
			if len(printable) > 0 {
				// Insert at cursor
				newBuf := make([]rune, len(l.buf)+len(printable))
				copy(newBuf, l.buf[:l.cursor])
				copy(newBuf[l.cursor:], printable)
				copy(newBuf[l.cursor+len(printable):], l.buf[l.cursor:])
				l.buf = newBuf
				l.cursor += len(printable)
				l.redraw("")
			}
		}
	}
}

func (l *liner) historyPrev() {
	if len(l.history) == 0 {
		return
	}
	if l.histPos < len(l.history)-1 {
		l.histPos++
	}
	l.buf = []rune(l.history[len(l.history)-1-l.histPos])
	l.cursor = len(l.buf)
	l.redraw("")
}

func (l *liner) historyNext() {
	if l.histPos <= 0 {
		l.histPos = -1
		l.buf = l.buf[:0]
		l.cursor = 0
		l.redraw("")
		return
	}
	l.histPos--
	l.buf = []rune(l.history[len(l.history)-1-l.histPos])
	l.cursor = len(l.buf)
	l.redraw("")
}

// redraw clears the current line and redraws prompt + buffer.
func (l *liner) redraw(_ string) {
	line := string(l.buf)

	// Build the part of the buffer before cursor for width calculation
	beforeCursor := string(l.buf[:l.cursor])
	cursorCol := l.printableLen(l.prompt) + l.printableLen(beforeCursor)

	// \r: go to column 0; \033[K: erase to end of line
	fmt.Printf("\r\033[K%s%s", l.prompt, line)

	// Move cursor to correct position
	lineLen := l.printableLen(l.prompt) + l.printableLen(line)
	if lineLen > cursorCol {
		// Move left by the difference
		fmt.Printf("\033[%dD", lineLen-cursorCol)
	}
}

// fallbackReadline is used when raw mode is unavailable.
func (l *liner) fallbackReadline() (string, error) {
	fmt.Print(l.prompt)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return scanner.Text(), nil
	}
	return "", fmt.Errorf("EOF")
}

// Close is a no-op (satisfies interface compatibility).
func (l *liner) Close() {}

// --- History ---

func (l *liner) addHistory(line string) {
	// Deduplicate: remove if already last entry
	if len(l.history) > 0 && l.history[len(l.history)-1] == line {
		return
	}
	l.history = append(l.history, line)
	if len(l.history) > 500 {
		l.history = l.history[len(l.history)-500:]
	}
	l.saveHistory()
}

func (l *liner) loadHistory() {
	if l.historyFile == "" {
		return
	}
	data, err := os.ReadFile(l.historyFile)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		if line != "" {
			l.history = append(l.history, line)
		}
	}
}

func (l *liner) saveHistory() {
	if l.historyFile == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(l.historyFile), 0700)
	_ = os.WriteFile(l.historyFile,
		[]byte(strings.Join(l.history, "\n")+"\n"), 0600)
}

func defaultHistoryFile() string {
	return filepath.Join(config.ConfigDir(), ".history")
}
