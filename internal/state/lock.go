// Package state guards on-disk identity and account mutations across processes.
package state

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type Lock struct {
	file *os.File
	once sync.Once
	err  error
}

// Acquire is nonblocking. Kernel locks are released automatically after a crash.
func Acquire(dir string) (*Lock, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "state.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = lockFile(f); err != nil {
		f.Close()
		return nil, fmt.Errorf("device state is already in use or cannot be locked: %w", err)
	}
	return &Lock{file: f}, nil
}
func (l *Lock) Close() error {
	l.once.Do(func() {
		if err := unlockFile(l.file); err != nil {
			l.err = err
		}
		if err := l.file.Close(); err != nil && l.err == nil {
			l.err = err
		}
	})
	return l.err
}
