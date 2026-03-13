package agent

import "fmt"

// TurnStats holds token counts for a single LLM call.
type TurnStats struct {
	PromptTokens     int
	CompletionTokens int
}

func (t TurnStats) Total() int { return t.PromptTokens + t.CompletionTokens }

func (t TurnStats) String() string {
	if t.Total() == 0 {
		return ""
	}
	return fmt.Sprintf("↑%d ↓%d", t.PromptTokens, t.CompletionTokens)
}

// SessionStats accumulates token counts across all turns in a session.
type SessionStats struct {
	Turns            int
	PromptTokens     int
	CompletionTokens int
}

func (s *SessionStats) Add(t TurnStats) {
	s.Turns++
	s.PromptTokens += t.PromptTokens
	s.CompletionTokens += t.CompletionTokens
}

func (s SessionStats) Total() int { return s.PromptTokens + s.CompletionTokens }

func (s SessionStats) String() string {
	if s.Total() == 0 {
		return ""
	}
	return fmt.Sprintf("session ↑%d ↓%d (%d turns)", s.PromptTokens, s.CompletionTokens, s.Turns)
}
