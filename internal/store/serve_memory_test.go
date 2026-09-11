package store

import (
	"context"
	"testing"
)

// THE SERVER'S CEILING IS THE SERVER'S, and the operator's wins over it. The
// writer's 16 GB default on two corpora is what took the served process to
// 39.8 GB committed on its first hybrid query.
func TestServeMemoryLimitPolicy(t *testing.T) {
	for raw, want := range map[string]string{
		"":       ServeMemoryLimit,
		"  ":     ServeMemoryLimit,
		"12GB":   "12GB",
		" 2GB  ": "2GB",
	} {
		if got := ServeMemoryLimitFor(raw); got != want {
			t.Errorf("ServeMemoryLimitFor(%q) = %q, want %q", raw, got, want)
		}
	}
	if ServeMemoryLimit == DefaultMemoryLimit {
		t.Error("the serve ceiling is the writer's default — then serve holds two writer-sized pools")
	}
}

// LimitMemory caps a store that is already open, which is the serve posture:
// OpenReadOnly has already applied the writer's default by then.
func TestLimitMemoryCapsAnOpenStore(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if err := s.LimitMemory("1500MB"); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT current_setting('memory_limit')`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "1.3 GiB" { // DuckDB reports the cap it applied, in its own units
		t.Errorf("memory_limit = %q after LimitMemory(1500MB), want 1.3 GiB", got)
	}
	if err := s.LimitMemory("not a size"); err == nil {
		t.Error("a limit DuckDB cannot parse must be reported, not swallowed")
	}
}
