package store

import (
	"io/fs"
	"os"
	"path/filepath"
)

type FileInfo struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type Store struct {
	basePath string
}

func New(basePath string) *Store {
	return &Store{basePath: basePath}
}

func (s *Store) Path(projectID string) string {
	return filepath.Join(s.basePath, projectID)
}

func (s *Store) List(projectID string) ([]FileInfo, error) {
	dir := s.Path(projectID)
	var files []FileInfo

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		relPath, _ := filepath.Rel(dir, path)
		files = append(files, FileInfo{
			Name: relPath,
			Size: info.Size(),
		})
		return nil
	})

	if os.IsNotExist(err) {
		return []FileInfo{}, nil
	}
	return files, err
}

func (s *Store) Get(projectID, path string) ([]byte, error) {
	fullPath := filepath.Join(s.Path(projectID), path)
	return os.ReadFile(fullPath)
}

func (s *Store) Add(projectID, path string, content []byte) error {
	fullPath := filepath.Join(s.Path(projectID), path)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return err
	}
	return os.WriteFile(fullPath, content, 0644)
}

func (s *Store) Delete(projectID, path string) error {
	fullPath := filepath.Join(s.Path(projectID), path)
	return os.Remove(fullPath)
}

func (s *Store) InitProjectContext(projectID string) error {
	return s.Add(projectID, "PROJECT.md", []byte("(empty)\n"))
}

func (s *Store) DeleteProject(projectID string) error {
	return os.RemoveAll(s.Path(projectID))
}
