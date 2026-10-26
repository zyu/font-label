package storage

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Store is a local-disk object store. Swap for S3 later behind the same API.
type Store struct {
	root string
}

func New(root string) (*Store, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, err
	}
	return &Store{root: abs}, nil
}

func (s *Store) Root() string { return s.root }

// Abs maps a relative path (e.g. 20261022/20261022_001.jpg) to an absolute one,
// refusing traversal outside the root.
func (s *Store) Abs(rel string) (string, error) {
	clean := path.Clean("/" + strings.ReplaceAll(rel, "\\", "/"))
	full := filepath.Join(s.root, filepath.FromSlash(clean))
	abs, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	if abs != s.root && !strings.HasPrefix(abs, s.root+string(os.PathSeparator)) {
		return "", errors.New("非法路径")
	}
	return abs, nil
}

func (s *Store) Write(rel string, data []byte) error {
	abs, err := s.Abs(rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	tmp := abs + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, abs)
}

func (s *Store) Open(rel string) (*os.File, error) {
	abs, err := s.Abs(rel)
	if err != nil {
		return nil, err
	}
	return os.Open(abs)
}

func (s *Store) Exists(rel string) bool {
	abs, err := s.Abs(rel)
	if err != nil {
		return false
	}
	st, err := os.Stat(abs)
	return err == nil && !st.IsDir()
}

func (s *Store) Remove(rel string) error {
	abs, err := s.Abs(rel)
	if err != nil {
		return err
	}
	if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *Store) Stat(rel string) (os.FileInfo, error) {
	abs, err := s.Abs(rel)
	if err != nil {
		return nil, err
	}
	return os.Stat(abs)
}
