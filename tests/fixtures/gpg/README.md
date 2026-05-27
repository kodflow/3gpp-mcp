# GPG fixture keyring

Throw-away keys for bats tests of `postCreate.sh::step_gpg_signing` (#366).
Never used outside `tests/`. Committed checked-in to avoid runtime
`--quick-generate-key` calls that block on CI entropy.

## Files

| File | Contents | UID |
|---|---|---|
| `pubkey.asc` | Public key (historical companion to a no-longer-committed secret) | `test-with-secret@example.com` |
| `pubkey-only.asc` | Public-only key (no companion secret available) | `test-pub-only@example.com` |

The previously-committed `seckey.asc` was replaced with runtime ed25519
generation in `make_test_gpg_keyring with-secret` — ed25519 needs far
less entropy than RSA, so generation is fast (<1s) even on entropy-starved
CI runners, and no private key material lives in source control. The
`pub-only` fixture remains static because the absence of a secret half
is the point of the test (the seckey-less / pub-only mode). `pubkey.asc`
is retained for any downstream tooling that wants a stable test-vector
pub key under the original UID, but the bats harness no longer imports it.

## Regeneration of `pubkey-only.asc`

```bash
GNUPGHOME=$(mktemp -d) && export GNUPGHOME && chmod 700 "$GNUPGHOME"
gpg --batch --pinentry-mode loopback --passphrase '' \
    --quick-generate-key "test-pub-only@example.com" rsa2048 default never
gpg --armor --export test-pub-only@example.com > pubkey-only.asc
```

## Security note

The remaining `pubkey-only.asc` is a public key with no companion secret
ever generated outside the original regeneration session. Treat its key
ID as a known "do-not-trust" identifier — never use it for real signing.
