package goal

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Lock prevents two concurrent runs from writing the same state.
//
// The naive implementation — create a file, delete it at the end — deadlocks the
// project the first time a run is killed: the file survives, and every later run
// refuses to start. The naive fix — delete any lock older than N minutes — is
// worse, because it happily stomps on a legitimately long GPU pass.
//
// So the lock records WHO holds it, and a contender decides by asking whether
// that process is still alive. Only a lock whose owner is provably gone is
// reclaimed; a live owner is always respected, no matter how long it has run.
type Lock struct {
	path string
	held bool
}

type lockInfo struct {
	PID       int       `json:"pid"`
	Host      string    `json:"host"`
	StartedAt time.Time `json:"started_at"`
	Command   string    `json:"command"`
}

// AcquireLock takes the pipeline lock, reclaiming it only from a dead owner.
func AcquireLock(local, command string) (*Lock, error) {
	dir := filepath.Join(local, "locks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "goal.lock")

	host, _ := os.Hostname()
	self := lockInfo{PID: os.Getpid(), Host: host, StartedAt: time.Now().UTC(), Command: command}

	for attempt := 0; attempt < 2; attempt++ {
		// O_EXCL is the actual mutual exclusion; everything else is diagnosis.
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			b, _ := json.MarshalIndent(self, "", "  ")
			_, _ = f.Write(b)
			_ = f.Sync()
			_ = f.Close()
			return &Lock{path: path, held: true}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}

		info, readErr := readLockInfo(path)
		switch {
		case readErr != nil:
			// Unreadable lock: it was created but not written (a crash between
			// O_EXCL and the write). Nobody can be relying on it.
			_ = os.Remove(path)
			continue
		case info.Host != host:
			return nil, fmt.Errorf("pipeline locked by %s (pid %d on %s, since %s) — refusing to reclaim a lock held on another machine",
				info.Command, info.PID, info.Host, info.StartedAt.Format(time.RFC3339))
		case info.PID == os.Getpid():
			// Re-entrant within the same process: treat as held.
			return &Lock{path: path, held: false}, nil
		case processAlive(info.PID):
			return nil, fmt.Errorf("pipeline already running: %s (pid %d, since %s). Wait for it, or stop it — a long GPU pass is normal",
				info.Command, info.PID, info.StartedAt.Format(time.RFC3339))
		default:
			// Owner is gone: stale lock from a killed run. Reclaim it, loudly.
			fmt.Fprintf(os.Stderr, "goal: reclaiming stale lock from dead pid %d (%s, started %s)\n",
				info.PID, info.Command, info.StartedAt.Format(time.RFC3339))
			_ = os.Remove(path)
			continue
		}
	}
	return nil, fmt.Errorf("could not acquire the pipeline lock at %s", path)
}

func readLockInfo(path string) (lockInfo, error) {
	var info lockInfo
	b, err := os.ReadFile(path)
	if err != nil {
		return info, err
	}
	if len(b) == 0 {
		return info, errors.New("empty lock")
	}
	if err := json.Unmarshal(b, &info); err != nil {
		return info, err
	}
	if info.PID <= 0 {
		return info, errors.New("lock has no pid")
	}
	return info, nil
}

// Release drops the lock. Safe to call on a re-entrant (non-owning) handle.
func (l *Lock) Release() {
	if l == nil || !l.held {
		return
	}
	_ = os.Remove(l.path)
	l.held = false
}
