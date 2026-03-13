package agent

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Spinner displays an animated indicator while the LLM is thinking.
// It is a no-op when the output is not a terminal (e.g. piped output).
type Spinner struct {
	out    io.Writer
	label  string
	done   chan struct{}
	mu     sync.Mutex
	active bool
	wg     sync.WaitGroup
}

func newSpinner(out io.Writer) *Spinner {
	return &Spinner{out: out}
}

// Start begins the animation. Safe to call multiple times (idempotent).
func (s *Spinner) Start(label string) {
	if !isTerminal(s.out) {
		return
	}
	s.mu.Lock()
	if s.active {
		s.mu.Unlock()
		return
	}
	s.active = true
	s.label = label
	s.done = make(chan struct{})
	s.mu.Unlock()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(80 * time.Millisecond)
		defer ticker.Stop()
		i := 0
		for {
			select {
			case <-s.done:
				return
			case <-ticker.C:
				s.mu.Lock()
				fmt.Fprintf(s.out, "\r\033[36m%s\033[0m %s",
					spinnerFrames[i%len(spinnerFrames)], s.label)
				s.mu.Unlock()
				i++
			}
		}
	}()
}

// Stop halts the animation and clears the spinner line.
// Safe to call when the spinner was never started or already stopped.
func (s *Spinner) Stop() {
	s.mu.Lock()
	if !s.active {
		s.mu.Unlock()
		return
	}
	s.active = false
	close(s.done)
	s.mu.Unlock()

	s.wg.Wait()
	fmt.Fprint(s.out, "\r\033[K") // erase the spinner line
}

// isTerminal reports whether w is writing to an interactive terminal.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
