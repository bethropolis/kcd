package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

var ErrStateLocked = errors.New("state is already locked")

// StateLock prevents two daemon instances from writing the same device state.
type StateLock struct {
	file *os.File
}

// AcquireStateLock locks the state file's sibling lock file without blocking.
func AcquireStateLock(statePath string) (*StateLock, error) {
	if err := os.MkdirAll(filepath.Dir(statePath), 0700); err != nil {
		return nil, fmt.Errorf("config: create state directory: %w", err)
	}
	path := statePath + ".lock"
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("config: open state lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrStateLocked
		}
		return nil, fmt.Errorf("config: lock state: %w", err)
	}
	return &StateLock{file: file}, nil
}

// Close releases the state lock.
func (l *StateLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	if err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN); err != nil {
		_ = l.file.Close()
		return fmt.Errorf("config: unlock state: %w", err)
	}
	return l.file.Close()
}
