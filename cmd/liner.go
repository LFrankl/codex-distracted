package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/term"

	"codex/config"
)

// liner is a minimal line editor with correct CJK/multi-byte handling.
type liner struct {
	historyFile string
	history     []string
	histPos     int
	buf         []rune
	cursor      int
	prompt      string
	statusBar   string // hint shown in bottom border
	reader      *bufio.Reader
	lastCtrlC   time.Time
	boxDrawn    bool // whether the 3-line box is currently on screen
}

func newLiner(historyFile string) *liner {
	l := &liner{
		historyFile: historyFile,
		histPos:     -1,
		reader:      bufio.NewReaderSize(os.Stdin, 256),
		statusBar:   "esc to interrupt  ↑↓ history",
	}
	l.loadHistory()
	return l
}

func (l *liner) SetPrompt(p string) { l.prompt = p }
func (l *liner) Close()             {}

// visWidth returns the terminal column width of a string,
// stripping ANSI escapes and counting CJK as 2 columns.
func visWidth(s string) int {
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
		if isWide(r) {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// isWide reports whether r occupies 2 terminal columns.
func isWide(r rune) bool {
	// CJK Unified Ideographs, Hiragana, Katakana, Hangul, full-width forms, etc.
	return r >= 0x1100 && (
		r <= 0x115F || // Hangul Jamo
			(r >= 0x2E80 && r <= 0x303E) || // CJK Radicals
			(r >= 0x3041 && r <= 0x33BF) || // Hiragana..CJK Compatibility
			(r >= 0x3400 && r <= 0x4DBF) || // CJK Extension A
			(r >= 0x4E00 && r <= 0x9FFF) || // CJK Unified Ideographs
			(r >= 0xA000 && r <= 0xA4CF) || // Yi
			(r >= 0xAC00 && r <= 0xD7AF) || // Hangul Syllables
			(r >= 0xF900 && r <= 0xFAFF) || // CJK Compatibility Ideographs
			(r >= 0xFE10 && r <= 0xFE6F) || // CJK Compatibility Forms
			(r >= 0xFF00 && r <= 0xFF60) || // Fullwidth Forms
			(r >= 0xFFE0 && r <= 0xFFE6) || // Fullwidth Signs
			(r >= 0x1F300 && r <= 0x1F9FF) || // Emoji
			(r >= 0x20000 && r <= 0x2FFFD) || // CJK Extension B-F
			(r >= 0x30000 && r <= 0x3FFFD))
}

func (l *liner) Readline() (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
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
	l.boxDrawn = false
	l.redraw()

	for {
		r, err := l.readNext()
		if err != nil {
			l.clearBox()
			return "", fmt.Errorf("EOF")
		}

		switch r {
		case '\r', '\n': // Enter
			line := string(l.buf)
			l.clearBox()
			if line != "" {
				l.addHistory(line)
			}
			return line, nil

		case 3: // Ctrl-C
			now := time.Now()
			if !l.lastCtrlC.IsZero() && now.Sub(l.lastCtrlC) < 1500*time.Millisecond {
				l.clearBox()
				return "", fmt.Errorf("EOF")
			}
			l.lastCtrlC = now
			l.buf = l.buf[:0]
			l.cursor = 0
			l.boxDrawn = false
			l.redraw()
			// print hint below box
			// Write hint on bottom border row, then return to input line.
			fmt.Print("\r\n\r\033[K\033[2m(press Ctrl+C again to exit)\033[0m\033[1A\r")

		case 4: // Ctrl-D
			l.clearBox()
			return "", fmt.Errorf("EOF")

		case 127, 8: // Backspace / DEL
			if l.cursor > 0 {
				l.buf = append(l.buf[:l.cursor-1], l.buf[l.cursor:]...)
				l.cursor--
				l.redraw()
			}

		case 21: // Ctrl-U
			l.buf = l.buf[:0]
			l.cursor = 0
			l.redraw()

		case 1: // Ctrl-A
			l.cursor = 0
			l.redraw()

		case 5: // Ctrl-E
			l.cursor = len(l.buf)
			l.redraw()

		case 23: // Ctrl-W: delete word before cursor
			i := l.cursor
			for i > 0 && l.buf[i-1] == ' ' {
				i--
			}
			for i > 0 && l.buf[i-1] != ' ' {
				i--
			}
			l.buf = append(l.buf[:i], l.buf[l.cursor:]...)
			l.cursor = i
			l.redraw()

		case runeUp: // ↑ history prev
			l.historyPrev()

		case runeDown: // ↓ history next
			l.historyNext()

		case runeRight: // → cursor right
			if l.cursor < len(l.buf) {
				l.cursor++
				l.redraw()
			}

		case runeLeft: // ← cursor left
			if l.cursor > 0 {
				l.cursor--
				l.redraw()
			}

		case runeDelete: // Delete key
			if l.cursor < len(l.buf) {
				l.buf = append(l.buf[:l.cursor], l.buf[l.cursor+1:]...)
				l.redraw()
			}

		default:
			if r >= 32 && utf8.ValidRune(r) {
				newBuf := make([]rune, len(l.buf)+1)
				copy(newBuf, l.buf[:l.cursor])
				newBuf[l.cursor] = r
				copy(newBuf[l.cursor+1:], l.buf[l.cursor:])
				l.buf = newBuf
				l.cursor++
				l.redraw()
			}
		}
	}
}

// Sentinel runes for escape sequences
const (
	runeUp     = rune(0x100001)
	runeDown   = rune(0x100002)
	runeLeft   = rune(0x100003)
	runeRight  = rune(0x100004)
	runeDelete = rune(0x100005)
)

// readNext reads one logical input event (rune or escape sequence) from stdin.
func (l *liner) readNext() (rune, error) {
	r, _, err := l.reader.ReadRune()
	if err != nil {
		return 0, err
	}

	if r != '\033' {
		return r, nil
	}

	// ESC — try to read the rest of the escape sequence (non-blocking peek)
	b1, err := l.reader.ReadByte()
	if err != nil || b1 != '[' {
		// bare ESC or unknown — ignore
		return 0, nil
	}

	b2, err := l.reader.ReadByte()
	if err != nil {
		return 0, nil
	}

	switch b2 {
	case 'A':
		return runeUp, nil
	case 'B':
		return runeDown, nil
	case 'C':
		return runeRight, nil
	case 'D':
		return runeLeft, nil
	case '3': // ESC[3~  = Delete
		l.reader.ReadByte() // consume '~'
		return runeDelete, nil
	default:
		// Consume rest of unknown sequence (up to a letter)
		for {
			b, err := l.reader.ReadByte()
			if err != nil || (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || b == '~' {
				break
			}
		}
	}
	return 0, nil
}

// redraw repaints the 3-line input box in-place.
// Invariant: cursor is on the input line (row 2) when this function returns.
func (l *liner) redraw() {
	w := termWidth()

	line := string(l.buf)
	beforeCursor := string(l.buf[:l.cursor])
	promptW := visWidth(l.prompt)
	cursorCol := promptW + visWidth(beforeCursor)

	topBorder := borderLine(w, "")
	bottomBorder := borderLine(w, l.statusBar)

	if l.boxDrawn {
		// Cursor is on input line. Jump up 1 to overwrite from top border.
		fmt.Print("\033[1A\r")
	}
	l.boxDrawn = true

	// Row 1: top border — print then move down (cursor → row 2 / input line)
	fmt.Printf("\r\033[K\033[2m%s\033[0m\r\n", topBorder)
	// Row 2: input line — print but stay here (no trailing \r\n yet)
	fmt.Printf("\r\033[K%s%s", l.prompt, line)
	// Position cursor within the input line.
	endCol := promptW + visWidth(line)
	if endCol > cursorCol {
		fmt.Printf("\033[%dD", endCol-cursorCol)
	}
	// Now move down to row 3 and print bottom border, then come back up.
	fmt.Printf("\r\n\r\033[K\033[2m%s\033[0m", bottomBorder)
	// Return to input line.
	fmt.Print("\033[1A\r")
	if cursorCol > 0 {
		fmt.Printf("\033[%dC", cursorCol)
	}
}

// clearBox erases the border lines, leaving the input line dimmed as history.
// Precondition: cursor is on the input line (row 2). Invariant from redraw().
func (l *liner) clearBox() {
	if !l.boxDrawn {
		return
	}
	// Clear top border.
	fmt.Print("\033[1A\r\033[K") // up 1, erase top border
	fmt.Print("\033[1B\r")       // back down to input line

	// Redraw input line as dimmed history — strip prompt ANSI and reprint dim.
	text := string(l.buf)
	fmt.Printf("\r\033[K\033[2m❯ %s\033[0m", text)

	// Clear bottom border.
	fmt.Print("\r\n\r\033[K")  // down to bottom border, erase it
	fmt.Print("\033[1A\r\n")   // up to input line, then past it
	l.boxDrawn = false
}

// termWidth returns the current terminal width, defaulting to 80.
func termWidth() int {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 {
		return 80
	}
	return w
}

// borderLine builds a full-width line of ─ chars with an optional right-aligned hint.
func borderLine(w int, hint string) string {
	hintW := visWidth(hint)
	dashes := w
	if hintW > 0 {
		dashes = w - hintW - 2
		if dashes < 1 {
			dashes = 1
		}
	}
	line := strings.Repeat("─", dashes)
	if hintW > 0 {
		line += "  " + hint
	}
	return line
}

func (l *liner) fallbackReadline() (string, error) {
	fmt.Print(l.prompt)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return scanner.Text(), nil
	}
	return "", fmt.Errorf("EOF")
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
	l.redraw()
}

func (l *liner) historyNext() {
	if l.histPos <= 0 {
		l.histPos = -1
		l.buf = l.buf[:0]
		l.cursor = 0
		l.redraw()
		return
	}
	l.histPos--
	l.buf = []rune(l.history[len(l.history)-1-l.histPos])
	l.cursor = len(l.buf)
	l.redraw()
}

func (l *liner) addHistory(line string) {
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
