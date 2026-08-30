# Labs pull & rotation — private `3gpp-mcp` image

All `3gpp-*` GHCR packages are **private** (they bake verbatim 3GPP specification
text — copyright 3GPP/ETSI, not redistributable publicly). The labs therefore
pulls with a **dedicated read-only token**, never anonymously and never with the
CI `GHCR_PAT` (which has write/admin scope).

There is **one** image to pull: `ghcr.io/kodflow/3gpp-mcp`. The data layer is
inherited by digest from `3gpp-data` and is already baked in — the labs does not
pull `3gpp-data` / `3gpp-vec` / `3gpp-corpus` directly.

| Tag | Contents | Size (pull) |
|---|---|---|
| `:latest` (alias `:full`) | binary + ONNX Runtime + fused corpus (lexical **+** vectors) + BGE-M3 fp32 | ~22 GB (once); code-only updates approx 150 MB |

A **code-only** rebuild reuses the data layer by digest, so after the first full
pull the labs only ever fetches the small top layers on updates.

## 1. Create the dedicated read-only token (once)

GitHub → Settings → Developer settings → **Fine-grained personal access token**:

- **Resource owner**: `kodflow`
- **Repository access**: Public repositories (read-only) is enough — package read
  is granted by the next line, not by repo access.
- **Permissions → Account / Packages**: **Read-only**.
- **Expiration**: 90 days (forces rotation — see §4).

Classic-PAT fallback (if fine-grained is unavailable): scope **`read:packages`**
only. Nothing else.

Store the value as the labs secret `GHCR_RO_TOKEN`. This token can **only pull**;
it cannot push, delete, or change visibility.

## 2. Pull — Docker / Compose

```bash
echo "$GHCR_RO_TOKEN" | docker login ghcr.io -u <github-username> --password-stdin
docker pull ghcr.io/kodflow/3gpp-mcp:latest
```

Pin by digest in production (immutable, audit-friendly):

```bash
docker pull ghcr.io/kodflow/3gpp-mcp@sha256:<digest>
```

Resolve the current digest with the same token:

```bash
crane digest ghcr.io/kodflow/3gpp-mcp:full     # crane auth login ghcr.io first
```

## 3. Pull — Kubernetes (`imagePullSecret`)

```bash
kubectl create secret docker-registry ghcr-3gpp \
  --docker-server=ghcr.io \
  --docker-username=<github-username> \
  --docker-password="$GHCR_RO_TOKEN" \
  -n <namespace>
```

Reference it in the pod/deployment:

```yaml
spec:
  imagePullSecrets:
    - name: ghcr-3gpp
  containers:
    - name: mcp-3gpp
      image: ghcr.io/kodflow/3gpp-mcp:full     # or pin @sha256:<digest>
```

## 4. Rotation

Two independent rotations — do not conflate them.

### 4a. Image rotation (new corpus or new code)

The CI publishes `:latest` (aliased `:full`) and dated tags. To roll forward:

```bash
docker pull ghcr.io/kodflow/3gpp-mcp:full     # re-pull moving tag
docker compose up -d                          # or: kubectl rollout restart deploy/mcp-3gpp
```

If you pin digests, bump the pinned `@sha256:` to the new `crane digest` output
and redeploy. A **code-only** change moves the tag but only ships ~150 MB of new
layers; a **corpus** change ships the full data layer once.

### 4b. Token rotation (every ≤90 days, or on suspected leak)

1. Generate a new read-only token (§1).
2. Update the labs secret:
   - Docker host: `docker login ghcr.io` again with the new value.
   - Kubernetes: `kubectl create secret docker-registry ghcr-3gpp ... --dry-run=client -o yaml | kubectl apply -f -` to replace in place, then `kubectl rollout restart` so pods pick up the new secret.
3. Delete the old token in GitHub.

Pulls in flight are unaffected; the new token authenticates the next pull.

## 5. Auto-update (a pull + a read token is all the labs needs)

The labs never rebuilds anything. The CI republishes the moving `:latest` tag
whenever the **code** or the **corpus** changes; the labs just re-pulls and
restarts. Pick one of:

**Docker host — a tiny systemd timer (or cron):**

```bash
# /etc/cron.daily/3gpp-mcp-update  (chmod +x)
#!/bin/sh
echo "$GHCR_RO_TOKEN" | docker login ghcr.io -u <user> --password-stdin
docker pull ghcr.io/kodflow/3gpp-mcp:full || exit 0   # no-op if unchanged
docker compose -f /opt/3gpp-mcp/docker-compose.yml up -d
docker image prune -f
```

A code-only republish ships only the small top layers (~150 MB); the ~14 GB data
layer is inherited by digest, so an unchanged corpus means the pull transfers
nothing. That is the whole point of the split — auto-update is cheap.

**Docker host — Watchtower (zero-script):**

```bash
docker run -d --name watchtower --restart unless-stopped \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v ~/.docker/config.json:/config.json:ro \
  containrrr/watchtower --interval 3600 --cleanup ghcr.io/kodflow/3gpp-mcp
```

**Kubernetes — pin the moving tag + a CronJob that restarts:**

```yaml
# image: ghcr.io/kodflow/3gpp-mcp:full  with imagePullPolicy: Always
# then a daily CronJob:
spec:
  schedule: "17 4 * * *"
  jobTemplate:
    spec:
      template:
        spec:
          serviceAccountName: mcp-restarter   # RBAC: patch deployments
          containers:
            - name: restart
              image: bitnami/kubectl
              command: ["kubectl","rollout","restart","deploy/mcp-3gpp"]
          restartPolicy: OnFailure
```

`imagePullPolicy: Always` + a rollout restart re-pulls `:full`; unchanged layers
are skipped. (Keep the `ghcr-3gpp` pull secret from §3 in the namespace.)

### What triggers a new publish

- **Code change** (push to `main` touching `cmd/server`, `internal/**`, the
  Dockerfile…) → `corpus-image.yml` rebuilds the small top layers and moves the
  tags. Cheap pull.
- **Corpus change** → the data image is re-baked, then `corpus-image.yml` rebuilds
  on the fresh data digest. One-time ~14 GB pull, then cheap again.

The labs side stays identical in both cases: `docker pull` + the read token.

## Anti-leak guarantee

Every workflow that pushes a `3gpp-*` package asserts the package is **private**
after the push (`Assert <pkg> is PRIVATE` — self-heal to private, then fail the
run loud if it cannot). So an accidental visibility flip is reverted on the next
publish and surfaced in CI — the labs token never becomes optional.
