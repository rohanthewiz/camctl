package cameras

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

const defaultFile = "cameras.json"

// Camera stores a named camera connection target.
type Camera struct {
	Label string `json:"label"`
	IP    string `json:"ip"`
	Port  int    `json:"port"`
}

// Store manages a persistent list of cameras backed by a JSON file.
type Store struct {
	mu      sync.RWMutex
	file    string
	cameras []Camera
}

// NewStore loads or creates the cameras file at filePath (defaults to cameras.json).
func NewStore(filePath string) (*Store, error) {
	if filePath == "" {
		filePath = defaultFile
	}
	s := &Store{file: filePath}
	if err := s.load(); err != nil {
		return nil, fmt.Errorf("cameras: %w", err)
	}
	return s, nil
}

// All returns a snapshot of the camera list.
func (s *Store) All() []Camera {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Camera, len(s.cameras))
	copy(out, s.cameras)
	return out
}

// Upsert adds a new camera or updates the entry with a matching label.
func (s *Store) Upsert(cam Camera) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, c := range s.cameras {
		if c.Label == cam.Label {
			s.cameras[i] = cam
			return s.save()
		}
	}
	s.cameras = append(s.cameras, cam)
	return s.save()
}

// Remove deletes the camera with the given label (no-op if not found).
func (s *Store) Remove(label string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, c := range s.cameras {
		if c.Label == label {
			s.cameras = append(s.cameras[:i], s.cameras[i+1:]...)
			return s.save()
		}
	}
	return nil
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.file)
	if os.IsNotExist(err) {
		s.cameras = []Camera{}
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &s.cameras)
}

func (s *Store) save() error {
	data, err := json.MarshalIndent(s.cameras, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.file, data, 0644)
}
