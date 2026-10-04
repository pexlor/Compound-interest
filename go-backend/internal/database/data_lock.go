package database

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// AcquireDataLock prevents two servers or a maintenance command from owning one
// SQLite directory at once. Keep the lock file in place to preserve its inode.
func AcquireDataLock(dataDir string) (*os.File, error) {
	if err := os.MkdirAll(dataDir, 0750); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(dataDir, ".fulibu.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, errors.New("SQLite data directory is in use; stop the server before maintenance")
	}
	return file, nil
}
