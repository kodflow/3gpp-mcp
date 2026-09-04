package goal

import (
	"strings"
	"testing"
)

// TestBuildServeKeepsTheInheritedLinkPath pins a step that could never have
// succeeded on this machine, and said nothing about it for days.
//
// Ctx.Run appends Cmd.Env to os.Environ(), and a later duplicate key wins, so a
// step that sets CGO_LDFLAGS discards the one the environment supplied. The
// Windows build needs BOTH directories: the embed-core cdylib comes out of the
// cargo target dir, and libduckdb comes from .local/toolchain/duckdb, which
// scripts/local/toolchain-env.sh exports because duckdb_use_lib links against a
// supplied library rather than the embedded archive.
//
// Naming only the first failed the link with "ld.exe: cannot find -lduckdb" every
// time. The step is Optional and the finish chain calls it with `|| echo`, so the
// failure never stopped anything — its recorded state was simply "never run", which
// reads like "not needed yet" rather than "has never worked".
func TestBuildServeKeepsTheInheritedLinkPath(t *testing.T) {
	t.Setenv("CGO_LDFLAGS", "-L/toolchain/duckdb")
	got := ldflagsWith("/cargo/release")
	if !strings.Contains(got, "-L/toolchain/duckdb") {
		t.Errorf("CGO_LDFLAGS = %q, dropping the inherited -L/toolchain/duckdb: the link fails with 'cannot find -lduckdb'", got)
	}
	if !strings.Contains(got, "-L/cargo/release") {
		t.Errorf("CGO_LDFLAGS = %q, missing the cdylib directory this step exists to add", got)
	}

	// With nothing inherited it must still be a valid single flag, not a stray
	// leading space that the linker would read as an empty argument.
	t.Setenv("CGO_LDFLAGS", "")
	if got := ldflagsWith("/cargo/release"); got != "-L/cargo/release" {
		t.Errorf("with no inherited flags, CGO_LDFLAGS = %q, want %q", got, "-L/cargo/release")
	}

	// Whitespace-only is the same case as empty: joining it would produce a
	// leading space and an argument the linker cannot parse.
	t.Setenv("CGO_LDFLAGS", "   ")
	if got := ldflagsWith("/cargo/release"); got != "-L/cargo/release" {
		t.Errorf("with whitespace-only inherited flags, CGO_LDFLAGS = %q, want %q", got, "-L/cargo/release")
	}
}
