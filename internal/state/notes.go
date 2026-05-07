package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// NotesPath places notes.json next to the state file.
func NotesPath(statePath string) string {
	if statePath == "" {
		return "notes.json"
	}
	dir := filepath.Dir(statePath)
	if dir == "" {
		dir = "."
	}
	return filepath.Join(dir, "notes.json")
}

// LoadNotes reads notes, treating a missing file as empty.
func LoadNotes(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	if len(b) == 0 {
		return map[string]string{}, nil
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]string{}
	}
	return m, nil
}

// SaveNote writes or clears one note via temp-file rename.
func SaveNote(path, sessionID, text string) error {
	if sessionID == "" {
		return nil
	}
	m, err := LoadNotes(path)
	if err != nil {
		return err
	}
	if text == "" {
		delete(m, sessionID)
	} else {
		m[sessionID] = text
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
