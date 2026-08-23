package goal

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Store persists step records under .local/state/steps/<name>.json.
//
// Durability matters more than speed here: a record claiming success for work
// that was not actually finished is worse than no record at all, because it
// makes the next run SKIP. Every write is therefore staged to a temporary file,
// fsynced, and renamed into place — a crash leaves either the old record or the
// new one, never a half-written one.
type Store struct{ dir string }

// NewStore roots a store at <local>/state/steps.
func NewStore(local string) (*Store, error) {
	dir := filepath.Join(local, "state", "steps")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

func (s *Store) path(step string) string {
	return filepath.Join(s.dir, sanitiseName(step)+".json")
}

// sanitiseName keeps step names usable as file names without ever mapping two
// distinct steps onto the same file.
func sanitiseName(n string) string {
	r := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_")
	return r.Replace(n)
}

// Load returns the persisted record, or (nil, nil) when the step has never run.
// A corrupt record is reported as an error rather than silently treated as
// absent: silently re-running is usually right, but silently hiding corruption
// is how a state machine loses the operator's trust.
func (s *Store) Load(step string) (*Record, error) {
	b, err := os.ReadFile(s.path(step))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rec Record
	if err := json.Unmarshal(b, &rec); err != nil {
		return nil, fmt.Errorf("state for %q is corrupt (%w) — delete %s to force a replay", step, err, s.path(step))
	}
	return &rec, nil
}

// Save writes the record atomically.
func (s *Store) Save(rec *Record) error {
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')

	final := s.path(rec.Step)
	tmp, err := os.CreateTemp(s.dir, ".tmp-"+sanitiseName(rec.Step)+"-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once renamed

	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	// fsync before rename: on a power loss, a renamed-but-unflushed file can be
	// an empty record, which reads as "never ran" and silently replays hours of
	// work. Cheap here, and the whole point of the exercise.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, final)
}

// All returns every persisted record, sorted by step name.
func (s *Store) All() (map[string]*Record, error) {
	ents, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	out := map[string]*Record{}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join(s.dir, n))
		if err != nil {
			continue
		}
		var rec Record
		if json.Unmarshal(b, &rec) != nil {
			continue
		}
		out[rec.Step] = &rec
	}
	return out, nil
}

// Forget deletes a record, forcing the step (and everything downstream) to be
// replayed. Used by `goal invalidate`.
func (s *Store) Forget(step string) error {
	err := os.Remove(s.path(step))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// WriteAtomic is the same tmp+fsync+rename discipline for a step's own outputs.
// Steps use it so a killed process never leaves a truncated artefact that a
// later run would mistake for a finished one.
func WriteAtomic(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
