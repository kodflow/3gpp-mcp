package bootstrap

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestVerifyORT(t *testing.T) {
	data := []byte("onnxruntime tarball bytes")
	sum := sha256.Sum256(data)
	ortSHA256["pkg-under-test"] = hex.EncodeToString(sum[:])
	defer delete(ortSHA256, "pkg-under-test")

	if err := verifyORT("pkg-under-test", data); err != nil {
		t.Fatalf("matching checksum should pass: %v", err)
	}
	if err := verifyORT("pkg-under-test", []byte("tampered")); err == nil {
		t.Fatal("checksum mismatch must fail closed")
	}
	if err := verifyORT("not-pinned", data); err == nil {
		t.Fatal("missing pin must fail closed (refuse unverified native lib)")
	}

	// The shipped pins are real 32-byte (64 hex char) sha256 digests.
	for pkg, h := range ortSHA256 {
		if pkg == "pkg-under-test" {
			continue
		}
		raw, err := hex.DecodeString(h)
		if err != nil || len(raw) != sha256.Size {
			t.Errorf("pin %q is not a sha256 hex digest: %q (err=%v)", pkg, h, err)
		}
	}
}
