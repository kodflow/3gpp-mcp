package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// imgtar oci --base <layout> --out <layout> <layer.tar.gz>…
//
// Writes an OCI image layout holding the base image with the layers appended —
// the image `crane append -b <base> -f <layer>…` builds, byte for byte — from the
// identity recorded next to each layer (identity.go), so no layer is read. The
// script then hands the layout to `crane push`, which uploads only the blobs the
// registry lacks, exactly as `crane append` did.
//
// WHY NOT crane append ANY MORE. crane cannot be told a layer's digest or diff_id:
// `-f` goes through tarball.LayerFromFile, which computes both, eagerly, by
// reading the file twice — 7m42 of the 2026-09-11 publish, for layers the
// registry already held. A layout is the one input crane takes whose descriptors
// it trusts as written: `crane push <dir>` reads the manifest and config from the
// layout and opens a layer only to upload it.
//
// BYTE FOR BYTE, BECAUSE THE DIGEST IS THE IMAGE. go-containerregistry does not
// copy the base's manifest and config: it decodes them into its own Go types and
// re-encodes them with encoding/json (mutate.image.compute), which drops fields it
// does not model, reorders the rest and, for this base, turns `"Entrypoint":[]`
// into nothing. The types below are the ones crane 0.20.2 decodes into
// (pkg/v1/config.go and manifest.go at v0.20.2, .local/bin/crane.exe's version),
// field for field and tag for tag, so the re-encoding is the same.
// TestAssemblyIsByteIdenticalToCraneAppend holds that against what crane actually
// pushed on 2026-09-11: manifest sha256:778f0d31…, config sha256:bcb41360….

const (
	mtOCIIndex    = "application/vnd.oci.image.index.v1+json"
	mtOCIManifest = "application/vnd.oci.image.manifest.v1+json"
	mtDockerMfst  = "application/vnd.docker.distribution.manifest.v2+json"
	mtOCILayer    = "application/vnd.oci.image.layer.v1.tar+gzip"
	mtDockerLayer = "application/vnd.docker.image.rootfs.diff.tar.gzip"
)

// ---- go-containerregistry v0.20.2 pkg/v1, mirrored -------------------------
//
// v1.Time is struct{ time.Time } and inherits time.Time's JSON methods; v1.Hash
// encodes as "sha256:<hex>" and refuses anything else on decode, which
// checkDigests reproduces.

type ociConfigFile struct {
	Architecture  string       `json:"architecture"`
	Author        string       `json:"author,omitempty"`
	Container     string       `json:"container,omitempty"`
	Created       time.Time    `json:"created,omitempty"`
	DockerVersion string       `json:"docker_version,omitempty"`
	History       []ociHistory `json:"history,omitempty"`
	OS            string       `json:"os"`
	RootFS        ociRootFS    `json:"rootfs"`
	Config        ociRunConfig `json:"config"`
	OSVersion     string       `json:"os.version,omitempty"`
	Variant       string       `json:"variant,omitempty"`
	OSFeatures    []string     `json:"os.features,omitempty"`
}

type ociHistory struct {
	Author     string    `json:"author,omitempty"`
	Created    time.Time `json:"created,omitempty"`
	CreatedBy  string    `json:"created_by,omitempty"`
	Comment    string    `json:"comment,omitempty"`
	EmptyLayer bool      `json:"empty_layer,omitempty"`
}

type ociRootFS struct {
	Type    string   `json:"type"`
	DiffIDs []string `json:"diff_ids"`
}

type ociHealthConfig struct {
	Test        []string      `json:",omitempty"`
	Interval    time.Duration `json:",omitempty"`
	Timeout     time.Duration `json:",omitempty"`
	StartPeriod time.Duration `json:",omitempty"`
	Retries     int           `json:",omitempty"`
}

type ociRunConfig struct {
	AttachStderr    bool                `json:"AttachStderr,omitempty"`
	AttachStdin     bool                `json:"AttachStdin,omitempty"`
	AttachStdout    bool                `json:"AttachStdout,omitempty"`
	Cmd             []string            `json:"Cmd,omitempty"`
	Healthcheck     *ociHealthConfig    `json:"Healthcheck,omitempty"`
	Domainname      string              `json:"Domainname,omitempty"`
	Entrypoint      []string            `json:"Entrypoint,omitempty"`
	Env             []string            `json:"Env,omitempty"`
	Hostname        string              `json:"Hostname,omitempty"`
	Image           string              `json:"Image,omitempty"`
	Labels          map[string]string   `json:"Labels,omitempty"`
	OnBuild         []string            `json:"OnBuild,omitempty"`
	OpenStdin       bool                `json:"OpenStdin,omitempty"`
	StdinOnce       bool                `json:"StdinOnce,omitempty"`
	Tty             bool                `json:"Tty,omitempty"`
	User            string              `json:"User,omitempty"`
	Volumes         map[string]struct{} `json:"Volumes,omitempty"`
	WorkingDir      string              `json:"WorkingDir,omitempty"`
	ExposedPorts    map[string]struct{} `json:"ExposedPorts,omitempty"`
	ArgsEscaped     bool                `json:"ArgsEscaped,omitempty"`
	NetworkDisabled bool                `json:"NetworkDisabled,omitempty"`
	MacAddress      string              `json:"MacAddress,omitempty"`
	StopSignal      string              `json:"StopSignal,omitempty"`
	Shell           []string            `json:"Shell,omitempty"`
}

type ociManifest struct {
	SchemaVersion int64             `json:"schemaVersion"`
	MediaType     string            `json:"mediaType,omitempty"`
	Config        ociDescriptor     `json:"config"`
	Layers        []ociDescriptor   `json:"layers"`
	Annotations   map[string]string `json:"annotations,omitempty"`
	Subject       *ociDescriptor    `json:"subject,omitempty"`
}

type ociIndex struct {
	SchemaVersion int64             `json:"schemaVersion"`
	MediaType     string            `json:"mediaType,omitempty"`
	Manifests     []ociDescriptor   `json:"manifests"`
	Annotations   map[string]string `json:"annotations,omitempty"`
	Subject       *ociDescriptor    `json:"subject,omitempty"`
}

type ociDescriptor struct {
	MediaType    string            `json:"mediaType"`
	Size         int64             `json:"size"`
	Digest       string            `json:"digest"`
	Data         []byte            `json:"data,omitempty"`
	URLs         []string          `json:"urls,omitempty"`
	Annotations  map[string]string `json:"annotations,omitempty"`
	Platform     *ociPlatform      `json:"platform,omitempty"`
	ArtifactType string            `json:"artifactType,omitempty"`
}

type ociPlatform struct {
	Architecture string   `json:"architecture"`
	OS           string   `json:"os"`
	OSVersion    string   `json:"os.version,omitempty"`
	OSFeatures   []string `json:"os.features,omitempty"`
	Variant      string   `json:"variant,omitempty"`
	Features     []string `json:"features,omitempty"`
}

// ---- the assembly ------------------------------------------------------------

// baseImage is the one image an OCI layout written by
// `crane pull --format=oci --platform <p> <ref>` holds.
type baseImage struct {
	dir       string
	mediaType string // the manifest's, as the layout's index names it
	manifest  ociManifest
	config    ociConfigFile
}

// appended is the image assemble produces: the raw bytes crane would push.
type appended struct {
	manifest, config []byte
	mediaType        string
	blobs            map[string]string // digest -> file holding it, for every layer
}

func (a appended) digest() string { return sha256Of(a.manifest) }

func sha256Of(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(s[:])
}

// blobPath is where an OCI layout keeps a blob.
func blobPath(dir, digest string) string {
	return filepath.Join(dir, "blobs", "sha256", strings.TrimPrefix(digest, "sha256:"))
}

// readBlob reads a blob of a layout and REFUSES it unless it hashes to its name.
// Both blobs read here are a few hundred bytes; checking them costs nothing and a
// wrong base is the one input this program would otherwise trust unexamined.
func readBlob(dir, digest string) ([]byte, error) {
	if !sha256Ref.MatchString(digest) {
		return nil, fmt.Errorf("%q is not a sha256 digest", digest)
	}
	b, err := os.ReadFile(blobPath(dir, digest))
	if err != nil {
		return nil, err
	}
	if got := sha256Of(b); got != digest {
		return nil, fmt.Errorf("blob %s in %s hashes to %s", digest, dir, got)
	}
	return b, nil
}

// decodeLikeGGCR decodes the way v1.ParseManifest and v1.ParseConfigFile do: one
// JSON value off a Decoder, unknown fields ignored.
func decodeLikeGGCR(b []byte, v any) error {
	return json.NewDecoder(bytes.NewReader(b)).Decode(v)
}

func loadBase(dir string) (baseImage, error) {
	bi := baseImage{dir: dir}
	raw, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		return bi, err
	}
	var idx ociIndex
	if err := decodeLikeGGCR(raw, &idx); err != nil {
		return bi, fmt.Errorf("%s/index.json: %w", dir, err)
	}
	// ONE IMAGE. crane pull appends to a layout that already exists, so a stale
	// directory would hold two bases, and which one this appended to would be an
	// accident of order.
	if len(idx.Manifests) != 1 {
		return bi, fmt.Errorf("%s holds %d manifests; the base layout must hold exactly one image "+
			"(crane pull --format=oci --platform …, into a fresh directory)", dir, len(idx.Manifests))
	}
	desc := idx.Manifests[0]
	if desc.MediaType != mtOCIManifest && desc.MediaType != mtDockerMfst {
		return bi, fmt.Errorf("%s holds a %q, not an image manifest (was --platform given to crane pull?)", dir, desc.MediaType)
	}
	bi.mediaType = desc.MediaType
	mb, err := readBlob(dir, desc.Digest)
	if err != nil {
		return bi, err
	}
	if err := decodeLikeGGCR(mb, &bi.manifest); err != nil {
		return bi, fmt.Errorf("base manifest: %w", err)
	}
	if err := checkDigests(bi.manifest); err != nil {
		return bi, fmt.Errorf("base manifest: %w", err)
	}
	cb, err := readBlob(dir, bi.manifest.Config.Digest)
	if err != nil {
		return bi, err
	}
	if err := decodeLikeGGCR(cb, &bi.config); err != nil {
		return bi, fmt.Errorf("base config: %w", err)
	}
	for _, d := range bi.config.RootFS.DiffIDs {
		if !sha256Ref.MatchString(d) {
			return bi, fmt.Errorf("base config: diff_id %q is not a sha256 digest", d)
		}
	}
	// crane append rewrites every layer it appends onto a Windows base
	// (internal/windows), and this does not.
	if bi.config.OS == "windows" {
		return bi, errors.New("the base is a Windows image; appending to it is not supported")
	}
	for _, l := range bi.manifest.Layers {
		if _, err := os.Stat(blobPath(dir, l.Digest)); err != nil {
			return bi, fmt.Errorf("the base layout lacks its layer %s, which the push may need to upload: %w", l.Digest, err)
		}
	}
	return bi, nil
}

// checkDigests refuses what v1.Hash would refuse to decode.
func checkDigests(m ociManifest) error {
	ds := append([]ociDescriptor{m.Config}, m.Layers...)
	if m.Subject != nil {
		ds = append(ds, *m.Subject)
	}
	for _, d := range ds {
		if !sha256Ref.MatchString(d.Digest) {
			return fmt.Errorf("digest %q is not sha256:<64 hex>", d.Digest)
		}
	}
	return nil
}

// assemble is mutate.AppendLayers(base, layers...) as crane.Append calls it,
// computed from recorded identities. Each step names the line of
// go-containerregistry v0.20.2 it reproduces.
func assemble(base baseImage, layers []string, ids []identity) (appended, error) {
	out := appended{mediaType: base.mediaType, blobs: map[string]string{}}
	for _, l := range base.manifest.Layers {
		out.blobs[l.Digest] = blobPath(base.dir, l.Digest)
	}

	// crane.Append: OCI layers onto an OCI base, Docker layers otherwise.
	layerType := mtDockerLayer
	if base.mediaType == mtOCIManifest {
		layerType = mtOCILayer
	}

	cfg := base.config // a copy; the slices below are rebuilt, never shared
	m := base.manifest
	diffIDs := append([]string(nil), cfg.RootFS.DiffIDs...)
	history := append([]ociHistory(nil), cfg.History...)
	descs := append([]ociDescriptor(nil), m.Layers...)
	for i, p := range layers {
		id := ids[i]
		if err := id.wellFormed(); err != nil {
			return out, fmt.Errorf("%s: %w", p, err)
		}
		// A LAYER THAT IS NOT COMPRESSED has a digest equal to its diff_id, and
		// would be declared tar+gzip below. imgtar oci only assembles gzip layers.
		if id.Digest == id.DiffID {
			return out, fmt.Errorf("%s: its digest is its diff_id, so it is not gzip-compressed", p)
		}
		// An Addendum with no History: one zero entry per layer, which is the
		// `{"created":"0001-01-01T00:00:00Z"}` every crane-appended layer carries.
		history = append(history, ociHistory{})
		diffIDs = append(diffIDs, id.DiffID)
		// tarball.layer.Descriptor(): size, digest, media type, no annotations.
		descs = append(descs, ociDescriptor{MediaType: layerType, Size: id.Size, Digest: id.Digest})
		if prev, dup := out.blobs[id.Digest]; dup && prev != p {
			// Two layers with one digest are one blob; the image is still valid.
			continue
		}
		out.blobs[id.Digest] = p
	}
	cfg.RootFS.DiffIDs = diffIDs
	cfg.History = history
	m.Layers = descs

	rcfg, err := json.Marshal(cfg)
	if err != nil {
		return out, err
	}
	m.Config.Digest = sha256Of(rcfg)
	m.Config.Size = int64(len(rcfg))
	// "If Data was set in the base image, we need to update it in the mutated
	// image." This base (debian, OCI) inlines its config, so this one matters.
	if base.manifest.Config.Data != nil {
		m.Config.Data = rcfg
	}
	// compute() sets the subject from the mutation, and AppendLayers sets none.
	m.Subject = nil

	rman, err := json.Marshal(m)
	if err != nil {
		return out, err
	}
	out.manifest, out.config = rman, rcfg
	return out, nil
}

// writeLayout writes img as an OCI image layout in dir, which must not exist:
// the blobs by hard link where the filesystem allows (no copy of a 23 GB layer),
// the config, the manifest, and an index naming that one manifest.
func writeLayout(dir string, img appended) error {
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("%s already exists; the layout is written into a fresh directory", dir)
	}
	if err := os.MkdirAll(filepath.Join(dir, "blobs", "sha256"), 0o755); err != nil {
		return err
	}
	for digest, src := range img.blobs {
		if err := linkOrCopy(src, blobPath(dir, digest)); err != nil {
			return err
		}
	}
	for _, b := range [][]byte{img.config, img.manifest} {
		if err := os.WriteFile(blobPath(dir, sha256Of(b)), b, 0o644); err != nil {
			return err
		}
	}
	idx, err := json.Marshal(ociIndex{
		SchemaVersion: 2,
		MediaType:     mtOCIIndex,
		Manifests: []ociDescriptor{{
			MediaType: img.mediaType,
			Size:      int64(len(img.manifest)),
			Digest:    img.digest(),
		}},
	})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "oci-layout"), []byte(`{"imageLayoutVersion":"1.0.0"}`), 0o644); err != nil {
		return err
	}
	// index.json LAST: `crane push` finds a layout by it, so a directory that has
	// one is a directory whose blobs are all in place.
	return os.WriteFile(filepath.Join(dir, "index.json"), idx, 0o644)
}

// layerIdentity returns the recorded identity of a layer, or computes it — the
// full read the record exists to avoid — when the record is missing or does not
// describe the file, and records it for next time.
func layerIdentity(layer string) (identity, bool, error) {
	id, err := readIdentity(layer)
	if err == nil {
		id.Key = ""
		return id, true, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "imgtar: %s: the identity record does not hold (%v)\n", filepath.Base(layer), err)
	}
	st, serr := os.Stat(layer)
	if serr != nil {
		return identity{}, false, serr
	}
	fmt.Printf("  %s: no valid identity record — reading %.1f MiB to compute its digest and diff_id\n",
		filepath.Base(layer), float64(st.Size())/(1<<20))
	id, err = computeIdentity(layer)
	if err != nil {
		return identity{}, false, err
	}
	return id, false, writeIdentity(layer, id)
}

func ociCmd(args []string) {
	fs := flag.NewFlagSet("oci", flag.ExitOnError)
	baseDir := fs.String("base", "", "OCI layout holding the base image alone (crane pull --format=oci --platform …)")
	outDir := fs.String("out", "", "OCI layout to write, for `crane push` (must not exist)")
	_ = fs.Parse(args)
	if *baseDir == "" || *outDir == "" || fs.NArg() == 0 {
		usage()
	}
	start := time.Now()
	base, err := loadBase(*baseDir)
	must(err)
	layers := fs.Args()
	ids := make([]identity, len(layers))
	for i, p := range layers {
		id, recorded, err := layerIdentity(p)
		must(err)
		ids[i] = id
		how := "recorded"
		if !recorded {
			how = "computed"
		}
		fmt.Printf("  %-22s %s  diff_id %s  %.1f MiB  (%s)\n", filepath.Base(p),
			short(id.Digest), short(id.DiffID), float64(id.Size)/(1<<20), how)
	}
	img, err := assemble(base, layers, ids)
	must(err)
	must(writeLayout(*outDir, img))
	fmt.Printf("  image %s  config %s  %d layer(s) onto %d base layer(s)  in %.1fs\n",
		img.digest(), sha256Of(img.config), len(layers), len(base.manifest.Layers), time.Since(start).Seconds())
}

func short(d string) string {
	if len(d) > 19 {
		return d[:19] + "…"
	}
	return d
}
