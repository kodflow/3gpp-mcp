package embed

// ffi_identity holds the cdylib-backend → canonical-identity mapping used by the
// embed_ffi build. It lives in an UNTAGGED, CGO-free file on purpose: the mapping
// is the load-bearing contract between the query embedder and the corpus, and it
// must be testable by a plain `go test ./...` — without cgo, without ONNX Runtime,
// and without having built rust/embed-core first.
//
// THE CONTRACT
//
// The corpus stamps schema_meta.embedding_model with the identity printed by
// `cmd/embedid`, i.e. ResolveModelID("bge-m3") — a 12-hex EmbedIdentity digest
// folding family, revision, tokenizer revision, dimension, normalisation,
// precision, windowing and max_tokens. At startup cmd/server compares that stamp
// against the live embedder's ModelID() and calls store.DisableVSS() when they
// differ (cmd/server/main.go, "semantic disabled: … (mismatch)").
//
// Returning a bare family name such as "bge-m3" here therefore does not merely
// look untidy: it makes the comparison fail for EVERY correctly-built corpus, so
// a DB carrying complete, valid vectors is served as pure lexical — silently.
// That regression shipped and went unnoticed because nothing pinned the two sides
// together; TestFFIModelIDMatchesStampedIdentity now does.

// backendHash is the string rust/embed-core reports when it is built WITHOUT the
// `ort` feature — the deterministic hash fallback that exists so the
// cdylib→cgo→Go seam can be exercised with no model and no GPU.
const backendHash = "hash"

// ffiModelID maps a cdylib backend string to the identity the corpus stamps for
// that backend. Anything that is not the hash fallback is the real BGE-M3 ONNX
// path (embed-core reports "bge-m3-onnx"), which must resolve to the SAME digest
// cmd/embedid prints — never to the bare family name.
func ffiModelID(backend string) string {
	if backend == backendHash {
		return ResolveModelID("hash-local")
	}
	return ResolveModelID("bge-m3")
}

// ffiSparseModelID maps a cdylib backend to the sparse identity the corpus stamps
// (schema_meta.sparse_model, written by `embed-io --import-sparse --sparse-model`
// from `cmd/embedid --sparse`). hasSparse reflects the LOADED model: a dense-only
// export has no sparse head, and the engine must then simply not offer the arm —
// hence "" rather than a digest that nothing could ever match.
func ffiSparseModelID(backend string, hasSparse bool) string {
	if !hasSparse {
		return ""
	}
	if backend == backendHash {
		return ResolveSparseID("hash-local")
	}
	return ResolveSparseID("bge-m3")
}
