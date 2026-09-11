#!/usr/bin/env bash
# Offline test for pin_corpus_snapshot (scripts/lib/corpus-pin.sh), the one writer
# of contracts/corpus-pin.txt. Run: bash scripts/lib/corpus-pin_test.sh
#
# What it guards: a publish that pushes a snapshot and then rewrites the wrong
# line, drops a comment, pins a TAG, or silently writes nothing leaves a fresh
# clone seeding a corpus nobody chose. The Go side (internal/goal/seed_pin_test.go)
# checks that what this writes is what the seed step reads.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../.." && pwd)"
# shellcheck source=scripts/lib/corpus-pin.sh
. "$HERE/corpus-pin.sh"

fails=0
pass() { echo "PASS  $1"; }
fail() { echo "FAIL  $1"; fails=$((fails + 1)); }

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

d() { local c="$1" s=""; for _ in $(seq 64); do s+="$c"; done; printf 'sha256:%s' "$s"; }
OLD3="ghcr.io/kodflow/3gpp-corpus@$(d 1)"
OLDE="ghcr.io/kodflow/etsi-corpus@$(d 2)"
NEWE="ghcr.io/kodflow/etsi-corpus@$(d 3)"

fixture() {
	printf '# header comment\n\n%s\n# between\n%s\n' "$OLD3" "$OLDE" >"$1"
}

# 1. The package's line moves; every other byte stays.
f="$work/pin1"
fixture "$f"
pin_corpus_snapshot "$f" etsi-corpus "$NEWE"
want="$(printf '# header comment\n\n%s\n# between\n%s\n' "$OLD3" "$NEWE")"
if [ "$(cat "$f")" = "$want" ]; then pass "only the ETSI line moved"; else fail "unexpected file after bump: $(cat "$f")"; fi

# 2-5. Refusals leave the file byte-identical.
refuse() { # $1 = label, $2 = package, $3 = ref
	local g="$work/refuse" before
	fixture "$g"
	before="$(cat "$g")"
	if pin_corpus_snapshot "$g" "$2" "$3" 2>/dev/null; then
		fail "$1: accepted"
	elif [ "$(cat "$g")" != "$before" ]; then
		fail "$1: refused but the file changed"
	else
		pass "$1: refused, file untouched"
	fi
}
refuse "a tag pins nothing" etsi-corpus "ghcr.io/kodflow/etsi-corpus:latest"
refuse "another package's digest" etsi-corpus "$OLD3"
refuse "a short digest" etsi-corpus "ghcr.io/kodflow/etsi-corpus@sha256:abc"
refuse "an uppercase digest" etsi-corpus "ghcr.io/kodflow/etsi-corpus@sha256:$(d A | cut -d: -f2)"
refuse "a nested owner" etsi-corpus "ghcr.io/a/b/etsi-corpus@$(d 3)"
refuse "not ghcr.io" etsi-corpus "docker.io/kodflow/etsi-corpus@$(d 3)"

# 6. A file with no line for the package is not silently extended.
g="$work/noline"
printf '%s\n' "$OLD3" >"$g"
if pin_corpus_snapshot "$g" etsi-corpus "$NEWE" 2>/dev/null; then fail "a missing line was accepted"; else pass "a missing line is refused"; fi

# 7. A CRLF checkout keeps its CRs, on the rewritten line too.
g="$work/crlf"
printf '# c\r\n%s\r\n%s\r\n' "$OLD3" "$OLDE" >"$g"
pin_corpus_snapshot "$g" etsi-corpus "$NEWE"
# Compared as FILES: a $(...) substitution may drop the CRs this is about (it
# does under Git Bash), and a test of line endings must not depend on the shell's.
printf '# c\r\n%s\r\n%s\r\n' "$OLD3" "$NEWE" >"$work/crlf.want"
if cmp -s "$g" "$work/crlf.want"; then pass "CRLF preserved"; else fail "CRLF not preserved"; fi

# 9. A held lock is honoured: the writer waits, then writes once the lock is gone.
g="$work/locked"
fixture "$g"
mkdir "$g.lock.d"
(PIN_LOCK_WAIT=30 pin_corpus_snapshot "$g" etsi-corpus "$NEWE") &
bg=$!
sleep 2
if grep -qF "$NEWE" "$g"; then fail "the writer did not wait for a held lock"; else pass "the writer waits while the lock is held"; fi
rmdir "$g.lock.d"
if wait "$bg" && grep -qF "$NEWE" "$g" && [ ! -d "$g.lock.d" ]; then
	pass "the writer writes once the lock is released, and releases it"
else
	fail "the writer did not complete after the lock was released"
fi

# 10. A lock that never frees fails the write and leaves both the file and the
# other writer's lock alone.
g="$work/stuck"
fixture "$g"
before="$(cat "$g")"
mkdir "$g.lock.d"
if PIN_LOCK_WAIT=1 pin_corpus_snapshot "$g" etsi-corpus "$NEWE" 2>/dev/null; then
	fail "a write went through a lock it never acquired"
elif [ "$(cat "$g")" = "$before" ] && [ -d "$g.lock.d" ]; then
	pass "a stuck lock fails the write, file and lock untouched"
else
	fail "a stuck lock changed the file or removed someone else's lock"
fi
rmdir "$g.lock.d"

# 11. A refused rewrite still releases the lock.
g="$work/refused"
printf '%s\n' "$OLD3" >"$g"
pin_corpus_snapshot "$g" etsi-corpus "$NEWE" 2>/dev/null || true
if [ -d "$g.lock.d" ]; then fail "a refused rewrite left its lock behind"; else pass "a refused rewrite releases its lock"; fi

# 12. Two publishes of different packages, side by side: both pins land.
g="$work/concurrent"
fixture "$g"
ok=1
for i in 4 5 6; do
	pin_corpus_snapshot "$g" 3gpp-corpus "ghcr.io/kodflow/3gpp-corpus@$(d "$i")" &
	p1=$!
	pin_corpus_snapshot "$g" etsi-corpus "ghcr.io/kodflow/etsi-corpus@$(d "$i")" &
	p2=$!
	wait "$p1" && wait "$p2" || ok=0
	grep -qF "ghcr.io/kodflow/3gpp-corpus@$(d "$i")" "$g" || ok=0
	grep -qF "ghcr.io/kodflow/etsi-corpus@$(d "$i")" "$g" || ok=0
done
if [ "$ok" = 1 ]; then pass "concurrent writers of two packages both land (3 rounds)"; else fail "a concurrent write lost a pin"; fi

# 8. The COMMITTED pin is writable by this tool: exactly one line per package.
g="$work/real"
cp "$ROOT/contracts/corpus-pin.txt" "$g"
if pin_corpus_snapshot "$g" 3gpp-corpus "ghcr.io/kodflow/3gpp-corpus@$(d 4)" &&
	pin_corpus_snapshot "$g" etsi-corpus "ghcr.io/kodflow/etsi-corpus@$(d 5)"; then
	pass "the committed pin has one line per package"
else
	fail "the committed pin cannot be bumped by publish-corpus.sh"
fi

if [ "$fails" -ne 0 ]; then
	echo "$fails failure(s)"
	exit 1
fi
echo "all corpus-pin tests passed"
