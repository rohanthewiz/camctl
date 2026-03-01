package presets

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

const (
	defaultFile = "presets.json"
	presetCount = 6
)

// Preset holds a camera preset slot number and its user-defined label.
type Preset struct {
	Number int    `json:"number"`
	Label  string `json:"label"`
}

// Store manages persistent preset labels backed by a JSON file.
// Thread-safe for concurrent handler access.
type Store struct {
	mu      sync.RWMutex
	file    string
	presets []Preset
}

// NewStore creates a Store that reads/writes from the given file path.
// If filePath is empty, it defaults to "presets.json" in the working directory.
// Loads existing data from disk or initializes defaults.
func NewStore(filePath string) (*Store, error) {
	if filePath == "" {
		filePath = defaultFile
	}
	s := &Store{file: filePath}
	if err := s.load(); err != nil {
		return nil, fmt.Errorf("presets: %w", err)
	}
	return s, nil
}

// All returns a copy of the current presets.
func (s *Store) All() []Preset {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Preset, len(s.presets))
	copy(out, s.presets)
	return out
}

// UpdateLabel changes the label for a preset by its slot number (0-based).
// Persists to disk immediately.
func (s *Store) UpdateLabel(num int, label string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if num < 0 || num >= len(s.presets) {
		return fmt.Errorf("presets: invalid preset number %d", num)
	}
	s.presets[num].Label = label
	return s.save()
}

// load reads presets from the JSON file, creating defaults if the file doesn't exist.
func (s *Store) load() error {
	data, err := os.ReadFile(s.file)
	if os.IsNotExist(err) {
		// First run — initialize with default labels
		s.presets = defaultPresets()
		return s.save()
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &s.presets)
}

// save writes the current presets to disk as indented JSON.
func (s *Store) save() error {
	data, err := json.MarshalIndent(s.presets, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.file, data, 0644)
}

// defaultPresets returns 6 presets with generic labels.
func defaultPresets() []Preset {
	presets := make([]Preset, presetCount)
	for i := range presets {
		presets[i] = Preset{
			Number: i,
			Label:  fmt.Sprintf("Preset %d", i+1),
		}
	}
	return presets
}
