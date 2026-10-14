//go:build windows

package state

// Windows does not support fsync on a Go directory handle. Files are flushed
// before replacement; interrupted account transitions are replayed at startup.
func SyncDirectory(path string) error { return nil }
