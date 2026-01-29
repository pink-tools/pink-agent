package state

import (
	"encoding/json"
	"os"
	"path/filepath"

	"pink-agent/internal/domain"
)

type Storage interface {
	Load() (*domain.State, error)
	Save(state *domain.State) error
}

type FileStorage struct {
	path string
}

func NewFileStorage(path string) *FileStorage {
	return &FileStorage{path: path}
}

func (s *FileStorage) Load() (*domain.State, error) {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return &domain.State{}, nil
	}
	if err != nil {
		return nil, err
	}

	var state domain.State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func (s *FileStorage) Save(state *domain.State) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}

	return os.Rename(tmp, s.path)
}
