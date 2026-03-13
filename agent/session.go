package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"codex/config"
	"codex/llm"
)

// Session is a saved conversation.
type Session struct {
	ID        string        `json:"id"`
	Provider  string        `json:"provider"`
	Model     string        `json:"model"`
	WorkDir   string        `json:"work_dir"`
	CreatedAt time.Time     `json:"created_at"`
	Messages  []llm.Message `json:"messages"`
}

func sessionsDir() string {
	return filepath.Join(config.ConfigDir(), "sessions")
}

func sessionPath(id string) string {
	return filepath.Join(sessionsDir(), id+".json")
}

// SaveSession writes the current messages to ~/.codex/sessions/<id>.json.
// If id is empty, a timestamp-based ID is generated. Returns the ID used.
func SaveSession(msgs []llm.Message, id, provider, model, workDir string) (string, error) {
	if id == "" {
		id = time.Now().Format("20060102-150405")
	}
	// Sanitize: replace spaces with dashes
	id = strings.ReplaceAll(id, " ", "-")

	if err := os.MkdirAll(sessionsDir(), 0700); err != nil {
		return "", err
	}

	s := Session{
		ID:        id,
		Provider:  provider,
		Model:     model,
		WorkDir:   workDir,
		CreatedAt: time.Now(),
		Messages:  msgs,
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", err
	}
	return id, os.WriteFile(sessionPath(id), data, 0600)
}

// LoadSession reads a saved session by ID.
func LoadSession(id string) (*Session, error) {
	data, err := os.ReadFile(sessionPath(id))
	if err != nil {
		return nil, fmt.Errorf("session %q not found", id)
	}
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse session: %w", err)
	}
	return &s, nil
}

// DeleteSession removes a saved session by ID.
func DeleteSession(id string) error {
	path := sessionPath(id)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("session %q not found", id)
	}
	return os.Remove(path)
}

// ListSessions returns all saved sessions, newest first.
func ListSessions() ([]Session, error) {
	entries, err := os.ReadDir(sessionsDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var sessions []Session
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		s, err := LoadSession(id)
		if err != nil {
			continue
		}
		sessions = append(sessions, *s)
	}

	// Sort newest first
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].CreatedAt.After(sessions[j].CreatedAt)
	})
	return sessions, nil
}
