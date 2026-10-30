package fish

import (
	"fmt"
	"os"
	"time"
)

// withCardsLock serialises read-modify-write updates of cards.jsonl across
// processes: the gateway, `picoclaw fish` and `picoclaw mcp serve` all write
// the same file. The lock is a lock file so it works on Linux and Windows.
func withCardsLock(workspace string, fn func() error) error {
	lockPath := CardsPath(workspace) + ".lock"
	if err := ensureDir(Dir(workspace)); err != nil {
		return err
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_ = f.Close()
			defer func() { _ = os.Remove(lockPath) }()
			return fn()
		}
		if !os.IsExist(err) {
			return err
		}
		// A crashed writer may leave the lock behind; reclaim it once it is old.
		if st, statErr := os.Stat(lockPath); statErr == nil && time.Since(st.ModTime()) > 60*time.Second {
			_ = os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("fish: timed out waiting for cards lock %s", lockPath)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
