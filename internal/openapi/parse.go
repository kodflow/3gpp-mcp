// Package openapi parses the canonical 3GPP 5GC OpenAPI YAML files published on
// 3GPP Forge into citable operations and schemas (axis #2). It is a deliberately
// minimal parser: it decodes only the fields needed for indexing + citation, not
// a full OpenAPI model. Pure Go (gopkg.in/yaml.v3), no CGO — the network fetch is
// a separate, offline step (scripts/fetch-5g-apis.sh).
package openapi

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// Result is the parse output for one YAML file.
type Result struct {
	SpecID     string
	Version    string // TS version from externalDocs, e.g. '18.13.0'
	Operations []model.APIOperation
	Schemas    []model.APISchema
}

// httpMethods are the path-item keys treated as operations (others — parameters,
// $ref, summary — are path-item metadata, not operations).
var httpMethods = map[string]bool{
	"get": true, "put": true, "post": true, "delete": true,
	"patch": true, "head": true, "options": true, "trace": true,
}

// externalDocs.description comes in two phrasings, both seen live:
//
//	"3GPP TS 29.518 V18.13.0; 5G System; ..."           (version glued to spec)
//	"3GPP TS 29.571 Common Data Types ..., version 18.11.0" (version at the end)
//
// so spec-id and version are matched independently.
var (
	reDocsSpec = regexp.MustCompile(`TS\s*(\d{2})\.(\d{3})`)
	reDocsVer  = regexp.MustCompile(`(?:[Vv]ersion\s+|\bV)(\d+\.\d+\.\d+)`)
)

// reFileSpec maps a filename token "TS29518" -> spec_id "29.518".
var reFileSpec = regexp.MustCompile(`^TS(\d{2})(\d{3})`)

type doc struct {
	Info struct {
		Version string `yaml:"version"`
	} `yaml:"info"`
	ExternalDocs struct {
		Description string `yaml:"description"`
	} `yaml:"externalDocs"`
	Servers []struct {
		URL string `yaml:"url"`
	} `yaml:"servers"`
	Paths      map[string]map[string]yaml.Node `yaml:"paths"`
	Components struct {
		Schemas map[string]yaml.Node `yaml:"schemas"`
	} `yaml:"components"`
}

type operationYAML struct {
	OperationID string   `yaml:"operationId"`
	Summary     string   `yaml:"summary"`
	Tags        []string `yaml:"tags"`
	RequestBody struct {
		Content map[string]struct {
			Schema yaml.Node `yaml:"schema"`
		} `yaml:"content"`
	} `yaml:"requestBody"`
	Responses map[string]yaml.Node `yaml:"responses"`
}

// ParseFile reads and parses a YAML file. release + sha come from the on-disk
// layout (data/sources/5g-apis/<Rel>/<sha>/<file>); they make every row
// reproducible and self-citing.
func ParseFile(path, release, sha string) (*Result, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(b, baseName(path), release, sha)
}

// Parse parses YAML bytes for a known filename/release/sha.
func Parse(data []byte, filename, release, sha string) (*Result, error) {
	var d doc
	if err := yaml.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("%s: %w", filename, err)
	}

	specID, version := identify(d, filename)
	if specID == "" {
		return nil, fmt.Errorf("%s: cannot determine spec_id (no externalDocs, no TS<nnnnn> filename)", filename)
	}
	service, family := serviceFromFile(filename)
	apiRoot := apiRootFromServers(d.Servers)
	forgeURL := model.ForgeRawURL(filename, sha, "")

	res := &Result{SpecID: specID, Version: version}

	// Operations: deterministic order (path, then method) for stable ids.
	paths := make([]string, 0, len(d.Paths))
	for p := range d.Paths {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		methods := make([]string, 0, len(d.Paths[p]))
		for m := range d.Paths[p] {
			if httpMethods[strings.ToLower(m)] {
				methods = append(methods, m)
			}
		}
		sort.Strings(methods)
		for _, m := range methods {
			node := d.Paths[p][m]
			var op operationYAML
			if node.Decode(&op) != nil {
				continue
			}
			res.Operations = append(res.Operations, model.APIOperation{
				SpecID: specID, Release: release, Version: version,
				APIDocVersion: d.Info.Version, Service: service, ServiceFamily: family,
				APIRoot: apiRoot, Path: p, Method: strings.ToUpper(m),
				OperationID:   op.OperationID,
				Summary:       op.Summary,
				Tags:          op.Tags,
				RequestSchema: requestSchema(op),
				ResponseCodes: responseCodes(op),
				YAMLFile:      filename, ForgeSHA: sha, ForgeURL: forgeURL,
			})
		}
	}

	// Schemas.
	names := make([]string, 0, len(d.Components.Schemas))
	for n := range d.Components.Schemas {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		node := d.Components.Schemas[n]
		sc := schemaFrom(n, node, specID)
		sc.SpecID, sc.Release, sc.Version = specID, release, version
		sc.Service = service
		sc.YAMLFile, sc.ForgeSHA = filename, sha
		sc.ForgeURL = model.ForgeRawURL(filename, sha, n)
		res.Schemas = append(res.Schemas, sc)
	}
	return res, nil
}

// identify returns (spec_id, version), preferring externalDocs (authoritative)
// and falling back to the filename token for spec_id (version stays empty then,
// honouring cite-or-silent — never fabricated).
func identify(d doc, filename string) (specID, version string) {
	desc := d.ExternalDocs.Description
	if m := reDocsSpec.FindStringSubmatch(desc); m != nil {
		specID = m[1] + "." + m[2]
		if v := reDocsVer.FindStringSubmatch(desc); v != nil {
			version = v[1]
		}
		return specID, version
	}
	if m := reFileSpec.FindStringSubmatch(filename); m != nil {
		return m[1] + "." + m[2], ""
	}
	return "", ""
}

// serviceFromFile maps "TS29518_Namf_Communication.yaml" -> ("Namf_Communication","Namf").
func serviceFromFile(filename string) (service, family string) {
	name := strings.TrimSuffix(filename, ".yaml")
	if i := strings.Index(name, "_"); i >= 0 {
		service = name[i+1:]
	}
	if service == "" {
		return "", ""
	}
	family = service
	if i := strings.Index(service, "_"); i >= 0 {
		family = service[:i]
	}
	return service, family
}

// apiRootFromServers strips a leading "{apiRoot}/" template token from the first
// server URL, e.g. "{apiRoot}/namf-comm/v1" -> "namf-comm/v1".
func apiRootFromServers(servers []struct {
	URL string `yaml:"url"`
}) string {
	if len(servers) == 0 {
		return ""
	}
	u := strings.TrimSpace(servers[0].URL)
	if i := strings.Index(u, "}/"); i >= 0 {
		return u[i+2:]
	}
	return strings.TrimPrefix(u, "/")
}

// requestSchema best-effort extracts the application/json request schema name.
func requestSchema(op operationYAML) string {
	c, ok := op.RequestBody.Content["application/json"]
	if !ok {
		for _, v := range op.RequestBody.Content { // any media type
			c = v
			break
		}
	}
	return firstRefName(&c.Schema)
}

func responseCodes(op operationYAML) []string {
	codes := make([]string, 0, len(op.Responses))
	for code := range op.Responses {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return codes
}

// schemaFrom builds an APISchema from its YAML node, classifying kind and
// collecting property names, enum literals and outgoing $ref edges.
func schemaFrom(name string, node yaml.Node, currentSpec string) model.APISchema {
	sc := model.APISchema{SchemaName: name}
	fields := mapFields(&node)
	if d, ok := fields["description"]; ok {
		sc.Description = strings.TrimSpace(d.Value)
	}
	if t, ok := fields["type"]; ok {
		sc.Kind = t.Value
	}
	if enum, ok := fields["enum"]; ok {
		sc.Kind = "enum"
		for _, e := range enum.Content {
			sc.EnumValues = append(sc.EnumValues, e.Value)
		}
	}
	if props, ok := fields["properties"]; ok && props.Kind == yaml.MappingNode {
		sc.Kind = orDefault(sc.Kind, "object")
		for i := 0; i+1 < len(props.Content); i += 2 {
			sc.Properties = append(sc.Properties, props.Content[i].Value)
		}
	}
	for _, comp := range []string{"allOf", "oneOf", "anyOf"} {
		if c, ok := fields[comp]; ok {
			sc.Kind = orDefault(sc.Kind, "composite")
			// anyOf/oneOf of {type:string, enum:[...]} -> capture enum values too
			for _, sub := range c.Content {
				sf := mapFields(sub)
				if enum, ok := sf["enum"]; ok {
					sc.Kind = "enum"
					for _, e := range enum.Content {
						sc.EnumValues = append(sc.EnumValues, e.Value)
					}
				}
			}
		}
	}
	sc.Kind = orDefault(sc.Kind, "object")

	refs := map[string]bool{}
	collectRefs(&node, currentSpec, refs)
	for r := range refs {
		sc.RefsOut = append(sc.RefsOut, r)
	}
	sort.Strings(sc.RefsOut)
	return sc
}

// mapFields indexes a mapping node's key->value children.
func mapFields(node *yaml.Node) map[string]*yaml.Node {
	out := map[string]*yaml.Node{}
	if node == nil || node.Kind != yaml.MappingNode {
		return out
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		out[node.Content[i].Value] = node.Content[i+1]
	}
	return out
}

// firstRefName returns the schema name of the first $ref found anywhere in a
// (possibly allOf/oneOf-wrapped) schema node, or "".
func firstRefName(node *yaml.Node) string {
	if node == nil {
		return ""
	}
	if node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			if node.Content[i].Value == "$ref" {
				return refSchemaName(node.Content[i+1].Value)
			}
		}
	}
	for _, c := range node.Content {
		if n := firstRefName(c); n != "" {
			return n
		}
	}
	return ""
}

// collectRefs walks a node, resolving every $ref to "<spec_id>:<SchemaName>".
func collectRefs(node *yaml.Node, currentSpec string, out map[string]bool) {
	if node == nil {
		return
	}
	if node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			if node.Content[i].Value == "$ref" {
				if e := refEdge(node.Content[i+1].Value, currentSpec); e != "" {
					out[e] = true
				}
			} else {
				collectRefs(node.Content[i+1], currentSpec, out)
			}
		}
		return
	}
	for _, c := range node.Content {
		collectRefs(c, currentSpec, out)
	}
}

// refEdge resolves an OpenAPI $ref to "<spec_id>:<SchemaName>". Same-file refs
// ("#/components/schemas/X") use currentSpec; cross-file refs
// ("TS29571_CommonData.yaml#/.../Uri") map the TS filename token to a spec_id.
// Non-TS or unparseable refs are dropped (no fabricated edges).
func refEdge(ref, currentSpec string) string {
	name := refSchemaName(ref)
	if name == "" {
		return ""
	}
	if strings.HasPrefix(ref, "#") {
		return currentSpec + ":" + name
	}
	file := ref
	if i := strings.Index(ref, "#"); i >= 0 {
		file = ref[:i]
	}
	file = baseName(file)
	if m := reFileSpec.FindStringSubmatch(file); m != nil {
		return m[1] + "." + m[2] + ":" + name
	}
	return ""
}

func refSchemaName(ref string) string {
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		return ref[i+1:]
	}
	return ""
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func baseName(p string) string {
	if i := strings.LastIndexAny(p, "/\\"); i >= 0 {
		return p[i+1:]
	}
	return p
}
