package transport

import (
	"os"
	"path/filepath"
)

// openCreate creates parent dirs and opens path for writing.
func openCreate(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return os.Create(path)
}
