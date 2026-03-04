package store

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

var ErrPathTraversal = errors.New("path escapes store directory")

type FileInfo struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type FileStore struct {
	basePath string
}

func New(basePath string) *FileStore {
	return &FileStore{basePath: basePath}
}

func (s *FileStore) Path(projectID string) string {
	return filepath.Join(s.basePath, projectID)
}

func (s *FileStore) List(projectID string) ([]FileInfo, error) {
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

func (s *FileStore) safePath(projectID, path string) (string, error) {
	dir := s.Path(projectID)
	full := filepath.Clean(filepath.Join(dir, path))
	if !strings.HasPrefix(full, dir+string(filepath.Separator)) && full != dir {
		return "", ErrPathTraversal
	}
	return full, nil
}

func (s *FileStore) Get(projectID, path string) ([]byte, error) {
	fullPath, err := s.safePath(projectID, path)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(fullPath)
}

func (s *FileStore) Add(projectID, path string, content []byte) error {
	fullPath, err := s.safePath(projectID, path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return err
	}
	return os.WriteFile(fullPath, content, 0644)
}

func (s *FileStore) Delete(projectID, path string) error {
	fullPath, err := s.safePath(projectID, path)
	if err != nil {
		return err
	}
	return os.Remove(fullPath)
}

func (s *FileStore) InitProject(projectID string) error {
	fullPath, err := s.safePath(projectID, "PROJECT.md")
	if err != nil {
		return err
	}
	if _, err := os.Stat(fullPath); err == nil {
		return nil // already exists
	}
	return s.Add(projectID, "PROJECT.md", []byte("(empty)\n"))
}

func (s *FileStore) DeleteProject(projectID string) error {
	return os.RemoveAll(s.Path(projectID))
}
