package agent

import (
	"fmt"
	"os"
	"time"
)

const maxUndoDepth = 20

type undoEntry struct {
	path      string
	content   []byte // nil means file did not exist before the write
	timestamp time.Time
}

// UndoStack is an in-memory stack of file backups.
// Before each write_file or patch_file, the original is pushed here.
type UndoStack struct {
	entries []undoEntry
}

// Push reads path from disk and saves it. If the file doesn't exist yet,
// a nil-content entry is stored so Undo can delete the file.
func (u *UndoStack) Push(path string) {
	data, err := os.ReadFile(path)
	entry := undoEntry{path: path, timestamp: time.Now()}
	if err == nil {
		entry.content = data
	}
	// Cap stack depth
	if len(u.entries) >= maxUndoDepth {
		u.entries = u.entries[1:]
	}
	u.entries = append(u.entries, entry)
}

// Pop restores the most recent backup. Returns a human-readable summary or an error.
func (u *UndoStack) Pop() (string, error) {
	if len(u.entries) == 0 {
		return "", fmt.Errorf("nothing to undo")
	}
	e := u.entries[len(u.entries)-1]
	u.entries = u.entries[:len(u.entries)-1]

	if e.content == nil {
		// File was created by the agent — delete it to undo
		if err := os.Remove(e.path); err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("undo delete %s: %w", e.path, err)
		}
		return fmt.Sprintf("Deleted %s (created at %s)", e.path, e.timestamp.Format("15:04:05")), nil
	}

	if err := os.WriteFile(e.path, e.content, 0644); err != nil {
		return "", fmt.Errorf("undo restore %s: %w", e.path, err)
	}
	return fmt.Sprintf("Restored %s (backup from %s)", e.path, e.timestamp.Format("15:04:05")), nil
}

// Len returns how many undo steps are available.
func (u *UndoStack) Len() int { return len(u.entries) }
