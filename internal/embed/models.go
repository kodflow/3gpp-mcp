package embed

import (
	_ "embed"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// models.yaml is the built-in default model registry. It is parsed at the same
// CGO-free layer the embedder identity lives in, so discover (no ONNX) and the real
// backend resolve the SAME active model — the published EmbedIdentity and the
// serve-time identity can't diverge.
//
//go:embed models.yaml
var defaultModelsYAML []byte

// ModelSpec declares ONE embedding model: where its files live, how its ONNX I/O is
// wired, and the components that define its EmbedIdentity. A registry of these (the
// embedded default + an optional user YAML) is what lets you swap BGE-M3 ↔ another
// model / precision by editing config instead of code.
type ModelSpec struct {
	Name              string   `yaml:"name"`               // registry key, e.g. "bge-m3", "bge-m3-fp16"
	Family            string   `yaml:"family"`             // identity family, e.g. "bge-m3"
	Dir               string   `yaml:"dir"`                // model dir: model.onnx [+ .onnx_data], tokenizer.json
	Precision         string   `yaml:"precision"`          // fp32 | fp16 (EmbedIdentity component)
	Dim               int      `yaml:"dim"`                // vector dimensionality
	Normalization     string   `yaml:"normalization"`      // "l2"
	Revision          string   `yaml:"revision"`           // weights revision (identity)
	TokenizerRevision string   `yaml:"tokenizer_revision"` // tokenizer revision (identity)
	TokenizerDir      string   `yaml:"tokenizer_dir"`      // optional; defaults to Dir
	Inputs            []string `yaml:"inputs"`             // ONNX input node names [ids, mask]
	Output            string   `yaml:"output"`             // ONNX output node name
}

// registryFile is the YAML shape: a set of models + which one is active.
type registryFile struct {
	Active string      `yaml:"active"`
	Models []ModelSpec `yaml:"models"`
}

// embedParts maps a model spec to the canonical EmbedParts (the identity inputs).
func (m ModelSpec) embedParts() model.EmbedParts {
	return model.EmbedParts{
		ModelID:           m.Family,
		ModelRevision:     m.Revision,
		TokenizerRevision: m.TokenizerRevision,
		VectorDim:         strconv.Itoa(m.Dim),
		NormalizationMode: m.Normalization,
		Precision:         m.Precision,
	}
}

// hardcodedBGE is the last-resort default if even the embedded YAML fails to parse
// (it never should — it's compiled in). Keeps the consts as the single pin source.
func hardcodedBGE() ModelSpec {
	return ModelSpec{
		Name: bgeModelName, Family: bgeModelName, Dir: filepath.Join("data", "models", "bge-m3"),
		Precision: PrecisionFP32, Dim: Dim, Normalization: bgeNormalization,
		Revision: BGEModelRevision, TokenizerRevision: BGETokenizerRevision,
		Inputs: []string{"input_ids", "attention_mask"}, Output: "sentence_embedding",
	}
}

// loadRegistry parses the embedded default, then overlays an optional user file
// (EMBED_MODELS_CONFIG, else data/models/models.yaml). Degrade-never-block: a bad
// override is logged and ignored; the defaults always stand.
func loadRegistry() registryFile {
	var reg registryFile
	if err := yaml.Unmarshal(defaultModelsYAML, &reg); err != nil || len(reg.Models) == 0 {
		reg = registryFile{Active: bgeModelName, Models: []ModelSpec{hardcodedBGE()}}
	}
	if path := overridePath(); path != "" {
		if b, err := os.ReadFile(path); err == nil {
			var over registryFile
			if err := yaml.Unmarshal(b, &over); err == nil {
				reg = mergeRegistry(reg, over)
			} else {
				log.Printf("embed: ignoring invalid model registry %s: %v", path, err)
			}
		}
	}
	return reg
}

// overridePath resolves the user registry file, if any.
func overridePath() string {
	if p := strings.TrimSpace(os.Getenv("EMBED_MODELS_CONFIG")); p != "" {
		return p
	}
	def := filepath.Join("data", "models", "models.yaml")
	if fi, err := os.Stat(def); err == nil && !fi.IsDir() {
		return def
	}
	return ""
}

// mergeRegistry overlays `over` onto `base`: models merge by name (override or add),
// and a non-empty `active` wins.
func mergeRegistry(base, over registryFile) registryFile {
	idx := make(map[string]int, len(base.Models))
	for i, m := range base.Models {
		idx[m.Name] = i
	}
	for _, m := range over.Models {
		if i, ok := idx[m.Name]; ok {
			base.Models[i] = m
		} else {
			base.Models = append(base.Models, m)
			idx[m.Name] = len(base.Models) - 1
		}
	}
	if strings.TrimSpace(over.Active) != "" {
		base.Active = over.Active
	}
	return base
}

// ActiveModel resolves the selected model: EMBED_MODEL env > registry `active` >
// "bge-m3". An unknown selection logs and falls back to the first registered model.
// CGO-free, so the embedder and discover agree on the identity.
func ActiveModel() ModelSpec {
	reg := loadRegistry()
	name := strings.TrimSpace(os.Getenv("EMBED_MODEL"))
	if name == "" {
		name = strings.TrimSpace(reg.Active)
	}
	if name == "" {
		name = bgeModelName
	}
	for _, m := range reg.Models {
		if m.Name == name {
			return m
		}
	}
	if len(reg.Models) > 0 {
		log.Printf("embed: model %q not in registry — using %q", name, reg.Models[0].Name)
		return reg.Models[0]
	}
	return hardcodedBGE()
}
