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
	statusFn    func() string // called each redraw for dynamic status bar content
	reader      *bufio.Reader
	lastCtrlC   time.Time
	boxDrawn    bool // whether the 3-line box is currently on screen

	// Tab completion
	completions []string
	tabState    int    // cycles through matches on successive Tabs
	tabPrefix   string // prefix at last Tab press
	tabMatches  []string

	// Ctrl+R reverse history search
	searching   bool
	searchQuery string
	searchMatch int    // index into history of current match (-1 = none)
	searchSaved []rune // original buffer before search

	// Bracketed paste mode
	inPaste bool // true while receiving bracketed paste content
}

func newLiner(historyFile string) *liner {
	l := &liner{
		historyFile: historyFile,
		histPos:     -1,
		reader:      bufio.NewReaderSize(os.Stdin, 256),
		searchMatch: -1,
	}
	l.loadHistory()
	return l
}

func (l *liner) SetPrompt(p string)              { l.prompt = p }
func (l *liner) SetStatusFn(fn func() string)    { l.statusFn = fn }
func (l *liner) SetCompletions(c []string)        { l.completions = c }
func (l *liner) Close()                           {}

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
	return r >= 0x1100 && (
		r <= 0x115F ||
			(r >= 0x2E80 && r <= 0x303E) ||
			(r >= 0x3041 && r <= 0x33BF) ||
			(r >= 0x3400 && r <= 0x4DBF) ||
			(r >= 0x4E00 && r <= 0x9FFF) ||
			(r >= 0xA000 && r <= 0xA4CF) ||
			(r >= 0xAC00 && r <= 0xD7AF) ||
			(r >= 0xF900 && r <= 0xFAFF) ||
			(r >= 0xFE10 && r <= 0xFE6F) ||
			(r >= 0xFF00 && r <= 0xFF60) ||
			(r >= 0xFFE0 && r <= 0xFFE6) ||
			(r >= 0x1F300 && r <= 0x1F9FF) ||
			(r >= 0x20000 && r <= 0x2FFFD) ||
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

	// Enable bracketed paste mode so pasted newlines don't trigger Enter.
	fmt.Print("\033[?2004h")
	defer fmt.Print("\033[?2004l")

	l.buf = l.buf[:0]
	l.cursor = 0
	l.histPos = -1
	l.boxDrawn = false
	l.tabState = -1
	l.searching = false
	l.inPaste = false
	l.redraw()

	for {
		r, err := l.readNext()
		if err != nil {
			l.clearBox()
			return "", fmt.Errorf("EOF")
		}

		// Any non-Tab key resets tab cycling state.
		if r != runTab {
			l.tabState = -1
		}
		// Any typing key exits search mode and keeps the current buffer.
		if l.searching && r != runeCtrlR && r != '\r' && r != '\n' &&
			r != 3 && r != 4 && r != runeEsc &&
			r != 127 && r != 8 && r != runeBackspace {
			l.exitSearch(true)
		}

		switch r {
		case runeStartPaste:
			l.inPaste = true

		case runeEndPaste:
			l.inPaste = false
			l.redraw()

		case '\r', '\n':
			if l.inPaste {
				// Inside a bracketed paste: insert a literal newline into the buffer.
				newBuf := make([]rune, len(l.buf)+1)
				copy(newBuf, l.buf[:l.cursor])
				newBuf[l.cursor] = '\n'
				copy(newBuf[l.cursor+1:], l.buf[l.cursor:])
				l.buf = newBuf
				l.cursor++
				l.redraw()
				continue
			}
			// Normal Enter: submit.
			if l.searching {
				l.exitSearch(true)
			}
			line := string(l.buf)
			l.clearBox()
			if line != "" {
				l.addHistory(line)
			}
			return line, nil

		case 3: // Ctrl-C
			if l.searching {
				l.exitSearch(false)
				l.redraw()
				continue
			}
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
			fmt.Print("\r\n\r\033[K\033[2m(press Ctrl+C again to exit)\033[0m\033[1A\r")

		case 4: // Ctrl-D
			l.clearBox()
			return "", fmt.Errorf("EOF")

		case 127, 8, runeBackspace: // Backspace
			if l.searching {
				if len(l.searchQuery) > 0 {
					l.searchQuery = l.searchQuery[:len(l.searchQuery)-1]
					l.updateSearch()
					l.redraw()
				}
				continue
			}
			if l.cursor > 0 {
				l.buf = append(l.buf[:l.cursor-1], l.buf[l.cursor:]...)
				l.cursor--
				l.redraw()
			}

		case 21: // Ctrl-U — clear line
			l.buf = l.buf[:0]
			l.cursor = 0
			l.redraw()

		case 11: // Ctrl-K — delete to end of line
			l.buf = l.buf[:l.cursor]
			l.redraw()

		case 1: // Ctrl-A / Home
			l.cursor = 0
			l.redraw()

		case 5: // Ctrl-E / End
			l.cursor = len(l.buf)
			l.redraw()

		case 2: // Ctrl-B — move left
			if l.cursor > 0 {
				l.cursor--
				l.redraw()
			}

		case 6: // Ctrl-F — move right
			if l.cursor < len(l.buf) {
				l.cursor++
				l.redraw()
			}

		case 12: // Ctrl-L — clear screen
			l.boxDrawn = false
			fmt.Print("\033[2J\033[H") // clear screen, move to top-left
			l.redraw()

		case 23: // Ctrl-W — delete word before cursor
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

		case runeCtrlR: // Ctrl-R — start/cycle reverse search
			if !l.searching {
				l.searchSaved = make([]rune, len(l.buf))
				copy(l.searchSaved, l.buf)
				l.searching = true
				l.searchQuery = ""
				l.searchMatch = -1
			} else {
				// Cycle to next older match.
				l.cycleSearch()
			}
			l.redraw()

		case runeEsc: // ESC — cancel search or no-op
			if l.searching {
				l.exitSearch(false)
				l.redraw()
			}

		case runTab: // Tab — command completion
			l.handleTab()

		case runeUp: // ↑ history prev
			if l.searching {
				l.exitSearch(true)
			}
			l.historyPrev()

		case runeDown: // ↓ history next
			if l.searching {
				l.exitSearch(true)
			}
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

		case runeWordRight: // Ctrl+Right — jump word right
			l.cursor = wordRight(l.buf, l.cursor)
			l.redraw()

		case runeWordLeft: // Ctrl+Left — jump word left
			l.cursor = wordLeft(l.buf, l.cursor)
			l.redraw()

		case runeHome: // Home
			l.cursor = 0
			l.redraw()

		case runeEnd: // End
			l.cursor = len(l.buf)
			l.redraw()

		case runeDelete: // Delete key
			if l.cursor < len(l.buf) {
				l.buf = append(l.buf[:l.cursor], l.buf[l.cursor+1:]...)
				l.redraw()
			}

		default:
			if l.searching {
				// In search mode, typing extends the query.
				if r >= 32 && utf8.ValidRune(r) {
					l.searchQuery += string(r)
					l.updateSearch()
					l.redraw()
				}
				continue
			}
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

// wordLeft returns the cursor position after jumping one word to the left.
func wordLeft(buf []rune, cursor int) int {
	i := cursor
	for i > 0 && buf[i-1] == ' ' {
		i--
	}
	for i > 0 && buf[i-1] != ' ' {
		i--
	}
	return i
}

// wordRight returns the cursor position after jumping one word to the right.
func wordRight(buf []rune, cursor int) int {
	n := len(buf)
	i := cursor
	for i < n && buf[i] != ' ' {
		i++
	}
	for i < n && buf[i] == ' ' {
		i++
	}
	return i
}

// handleTab performs command completion if the buffer starts with '/'.
func (l *liner) handleTab() {
	if len(l.completions) == 0 {
		return
	}
	input := string(l.buf[:l.cursor])
	if !strings.HasPrefix(input, "/") {
		return
	}

	// Rebuild match list if prefix changed.
	if l.tabState < 0 || input != l.tabPrefix {
		l.tabPrefix = input
		l.tabMatches = l.tabMatches[:0]
		for _, c := range l.completions {
			if strings.HasPrefix(c, input) && c != input {
				l.tabMatches = append(l.tabMatches, c)
			}
		}
		l.tabState = -1
	}
	if len(l.tabMatches) == 0 {
		return
	}

	l.tabState = (l.tabState + 1) % len(l.tabMatches)
	completed := []rune(l.tabMatches[l.tabState])
	// Replace everything up to cursor with the completion.
	tail := l.buf[l.cursor:]
	l.buf = append(completed, tail...)
	l.cursor = len(completed)
	l.redraw()
}

// Search mode helpers.

func (l *liner) updateSearch() {
	l.searchMatch = -1
	if l.searchQuery == "" {
		l.buf = append(l.buf[:0], l.searchSaved...)
		l.cursor = len(l.buf)
		return
	}
	for i := len(l.history) - 1; i >= 0; i-- {
		if strings.Contains(l.history[i], l.searchQuery) {
			l.searchMatch = i
			l.buf = []rune(l.history[i])
			l.cursor = len(l.buf)
			return
		}
	}
	// No match — keep showing query but leave buffer as-is.
}

func (l *liner) cycleSearch() {
	if l.searchQuery == "" || l.searchMatch <= 0 {
		return
	}
	for i := l.searchMatch - 1; i >= 0; i-- {
		if strings.Contains(l.history[i], l.searchQuery) {
			l.searchMatch = i
			l.buf = []rune(l.history[i])
			l.cursor = len(l.buf)
			return
		}
	}
}

func (l *liner) exitSearch(keep bool) {
	l.searching = false
	if !keep {
		l.buf = append(l.buf[:0], l.searchSaved...)
		l.cursor = len(l.buf)
	}
	l.searchQuery = ""
	l.searchMatch = -1
}

// Sentinel runes for escape sequences
const (
	runeUp         = rune(0x100001)
	runeDown       = rune(0x100002)
	runeLeft       = rune(0x100003)
	runeRight      = rune(0x100004)
	runeDelete     = rune(0x100005)
	runeWordLeft   = rune(0x100006)
	runeWordRight  = rune(0x100007)
	runeHome       = rune(0x100008)
	runeEnd        = rune(0x100009)
	runeCtrlR      = rune(0x10000A)
	runeEsc        = rune(0x10000B)
	runTab         = rune(0x10000C)
	runeBackspace  = rune(0x10000D)
	runeStartPaste = rune(0x10000E) // ESC[200~ bracketed paste start
	runeEndPaste   = rune(0x10000F) // ESC[201~ bracketed paste end
)

// readNext reads one logical input event (rune or escape sequence) from stdin.
func (l *liner) readNext() (rune, error) {
	r, _, err := l.reader.ReadRune()
	if err != nil {
		return 0, err
	}

	switch r {
	case '\t':
		return runTab, nil
	case 18: // Ctrl-R
		return runeCtrlR, nil
	}

	if r != '\033' {
		return r, nil
	}

	// ESC — try to read the rest of the escape sequence.
	b1, err := l.reader.ReadByte()
	if err != nil {
		return runeEsc, nil // bare ESC
	}

	// ESC O sequences (SS3 — used by some terminals for Home/End/arrows)
	if b1 == 'O' {
		b2, err := l.reader.ReadByte()
		if err != nil {
			return runeEsc, nil
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
		case 'H':
			return runeHome, nil
		case 'F':
			return runeEnd, nil
		}
		return 0, nil
	}

	// ESC [ sequences (CSI)
	if b1 != '[' {
		// ESC b / ESC f — Alt+Left/Right (some terminals)
		if b1 == 'b' {
			return runeWordLeft, nil
		}
		if b1 == 'f' {
			return runeWordRight, nil
		}
		return runeEsc, nil
	}

	// Read parameter bytes then the final byte.
	var params []byte
	for {
		b, err := l.reader.ReadByte()
		if err != nil {
			return 0, nil
		}
		if (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || b == '~' {
			// Final byte.
			switch b {
			case 'A':
				return runeUp, nil
			case 'B':
				return runeDown, nil
			case 'C':
				if hasModifier5(params) {
					return runeWordRight, nil
				}
				return runeRight, nil
			case 'D':
				if hasModifier5(params) {
					return runeWordLeft, nil
				}
				return runeLeft, nil
			case 'H':
				return runeHome, nil
			case 'F':
				return runeEnd, nil
			case '~':
				ps := string(params)
				switch ps {
				case "1", "7":
					return runeHome, nil
				case "4", "8":
					return runeEnd, nil
				case "3":
					return runeDelete, nil
				case "5":
					return runeWordRight, nil // Ctrl+Right on some terms
				case "6":
					return runeWordLeft, nil  // Ctrl+Left on some terms
				case "200":
					return runeStartPaste, nil
				case "201":
					return runeEndPaste, nil
				}
			}
			return 0, nil
		}
		params = append(params, b)
	}
}

// hasModifier5 checks if params encode modifier ;5 (Ctrl key).
// E.g. ESC[1;5C = Ctrl+Right.
func hasModifier5(params []byte) bool {
	s := string(params)
	return strings.Contains(s, ";5") || strings.Contains(s, ";6") ||
		s == "5" || s == "6"
}

// statusLine returns the content for the bottom border.
func (l *liner) statusLine() string {
	if l.searching {
		indicator := "no match"
		if l.searchMatch >= 0 {
			indicator = fmt.Sprintf("match %d/%d", len(l.history)-l.searchMatch, len(l.history))
		}
		return fmt.Sprintf("\033[33msearch:\033[0m\033[2m %s  [%s]  ESC to cancel\033[0m",
			l.searchQuery, indicator)
	}
	if l.statusFn != nil {
		if s := l.statusFn(); s != "" {
			return "\033[2m" + s + "\033[0m"
		}
	}
	return "\033[2m↑↓ history  Ctrl+R search  Tab complete\033[0m"
}

// displayRunes converts buf runes to a display string, replacing \n with a dim ↵ marker.
func displayRunes(runes []rune) string {
	var sb strings.Builder
	for _, r := range runes {
		if r == '\n' {
			sb.WriteString("\033[2m↵\033[0m")
		} else {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// redraw repaints the 3-line input box in-place.
func (l *liner) redraw() {
	w := termWidth()

	line := displayRunes(l.buf)
	beforeCursor := displayRunes(l.buf[:l.cursor])
	promptW := visWidth(l.prompt)
	cursorCol := promptW + visWidth(beforeCursor)

	status := l.statusLine()
	topBorder := borderLine(w, "")
	bottomBorder := borderLineLeft(w, status)

	if l.boxDrawn {
		fmt.Print("\033[1A\r")
	}
	l.boxDrawn = true

	fmt.Printf("\r\033[K\033[2m%s\033[0m\r\n", topBorder)
	fmt.Printf("\r\033[K%s%s", l.prompt, line)
	endCol := promptW + visWidth(line)
	if endCol > cursorCol {
		fmt.Printf("\033[%dD", endCol-cursorCol)
	}
	fmt.Printf("\r\n\r\033[K%s", bottomBorder)
	fmt.Print("\033[1A\r")
	if cursorCol > 0 {
		fmt.Printf("\033[%dC", cursorCol)
	}
}

// clearBox erases the border lines, leaving the input line dimmed as history.
func (l *liner) clearBox() {
	if !l.boxDrawn {
		return
	}
	fmt.Print("\033[1A\r\033[K")
	fmt.Print("\033[1B\r")
	text := string(l.buf)
	fmt.Printf("\r\033[K\033[2m❯ %s\033[0m", text)
	fmt.Print("\r\n\r\033[K")
	fmt.Print("\033[1A\r\n")
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

// borderLine builds a full-width line of ─ chars.
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

// borderLineLeft builds the bottom border with content left-aligned (for status).
func borderLineLeft(w int, content string) string {
	contentW := visWidth(content)
	remaining := w - contentW
	if remaining < 0 {
		remaining = 0
	}
	return content + strings.Repeat("─", remaining)
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
			// Unescape \001 back to newline (multi-line entries).
			l.history = append(l.history, strings.ReplaceAll(line, "\x01", "\n"))
		}
	}
}

func (l *liner) saveHistory() {
	if l.historyFile == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(l.historyFile), 0700)
	lines := make([]string, len(l.history))
	for i, h := range l.history {
		// Escape literal newlines so the history file stays line-delimited.
		lines[i] = strings.ReplaceAll(h, "\n", "\x01")
	}
	_ = os.WriteFile(l.historyFile,
		[]byte(strings.Join(lines, "\n")+"\n"), 0600)
}

func defaultHistoryFile() string {
	return filepath.Join(config.ConfigDir(), ".history")
}
