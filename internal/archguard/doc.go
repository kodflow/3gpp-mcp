// Package archguard holds compile-and-test-time architecture invariants that the
// type system alone cannot express. It carries no runtime code — only guard tests.
//
// The keystone invariant (migration plan Phase 11a, finding A14) is "the served Go
// binary never writes the DuckDB": Rust owns the write-side, Go opens the DB
// read-only and answers queries. cmd/server already defaults to store.OpenReadOnly
// (a --writable maintenance escape hatch aside), but that is a wiring choice, not a
// guarantee — a future handler could call a write method on the *store.Store it was
// handed and silently corrupt the read-mostly serve posture.
//
// A type-checked callgraph guard (go/ssa) is impractical here because the store
// package uses CGO (DuckDB), which go/types cannot import without the C toolchain in
// the test sandbox. So the guard is LEXICAL (the plan's documented forbidigo
// fallback): it asserts the request-path packages reference neither the read-write
// store.Open constructor nor any store write method in their non-test code.
package archguard
