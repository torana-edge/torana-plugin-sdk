package providerschema

// Parsing of the VENDORED provider protos (source/*.proto, pinned by
// source/manifest.json). This is the only place schema facts come from:
// snapshot.gen.go is generated from these bytes, and the offline tests
// re-derive the node set from them — a hand-edited generated table or a
// tampered artifact fails the inventory.
//
// The parser is deliberately small and strict: it handles the constructs
// these three pinned files use (top-level messages, oneof field blocks,
// nested messages/enums, enum values). It does NOT pretend to be a full
// protobuf parser; regeneration against a materially different upstream
// file is a reviewed act (generate.sh + manifest update).

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Surfaces (wire surfaces of the two pinned googleapis packages).
const (
	surfaceGemini = "gemini"
	surfaceVertex = "vertex"
)

// manifestFile is the vendored provenance manifest.
var manifestFile = filepath.Join("source", "manifest.json")

// ManifestEntry is one vendored artifact's provenance.
type ManifestEntry struct {
	Local              string `json:"local"`
	UpstreamCommitSHA  string `json:"upstream_commit_sha"`
	UpstreamCommitDate string `json:"upstream_commit_date"`
	Path               string `json:"path"`
	URL                string `json:"url"`
	SHA256             string `json:"sha256"`
}

// Manifest is the parsed vendored manifest.
type Manifest struct {
	FetchedAt string          `json:"fetched_at"`
	License   string          `json:"license"`
	Upstream  string          `json:"upstream"`
	Files     []ManifestEntry `json:"files"`
}

// LoadManifest reads the vendored manifest.
func LoadManifest() (*Manifest, error) {
	raw, err := os.ReadFile(manifestFile)
	if err != nil {
		return nil, fmt.Errorf("read vendored manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse vendored manifest: %w", err)
	}
	return &m, nil
}

// VerifyVendoredDigests recomputes the sha256 of every vendored artifact
// and compares it with the manifest. A tampered or un-re-vendored artifact
// fails here.
func VerifyVendoredDigests(m *Manifest) error {
	for _, f := range m.Files {
		raw, err := os.ReadFile(filepath.Join("source", f.Local))
		if err != nil {
			return fmt.Errorf("read vendored %s: %w", f.Local, err)
		}
		sum := sha256.Sum256(raw)
		got := hex.EncodeToString(sum[:])
		if got != f.SHA256 {
			return fmt.Errorf("vendored %s digest %s != manifest %s (re-vendor and regenerate)", f.Local, got, f.SHA256)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// proto text parsing (constrained to the pinned artifacts)
// ---------------------------------------------------------------------------

// extractMessage returns the BODY of the first top-level `message NAME {`
// in s (excluding the braces), or "" when absent.
func extractMessage(s, name string) string {
	re := regexp.MustCompile(`(?m)^message ` + regexp.QuoteMeta(name) + ` \{`)
	m := re.FindStringIndex(s)
	if m == nil {
		return ""
	}
	return braceBody(s[m[0]+len("message ")+len(name)+2:])
}

// braceBody consumes a balanced `{ ... }` starting at the text immediately
// after the opening brace and returns the body.
func braceBody(s string) string {
	depth := 1
	i := 0
	for depth > 0 && i < len(s) {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
		}
		i++
	}
	return s[:i-1]
}

// fieldDesc is one parsed field.
type fieldDesc struct {
	Repeated bool
	Optional bool
	Type     string
	Name     string
	Number   string
}

// fieldRe is line-anchored (re.M) and may span a wrapped field option
// (`= 6\n    [(google.api.field_behavior) = OPTIONAL];`).
var fieldRe = regexp.MustCompile(`(?m)^\s*(repeated\s+|optional\s+)?([\w.]+)\s+(\w+)\s*=\s*(\d+)\s*(\[[^\]]*\])?\s*;`)

// messageFields returns the fields declared directly in a message body,
// INCLUDING fields inside oneof blocks (arms), EXCLUDING fields of nested
// messages/enums. The oneof grouping itself is not a schema fact the
// inventory needs; only the members are.
func messageFields(body string) []fieldDesc {
	// Strip nested message/enum bodies so their fields cannot leak into
	// the parent (e.g. Part.MediaResolution.level is NOT a Part field).
	var stripped []byte
	for i := 0; i < len(body); {
		m := regexp.MustCompile(`(?m)^(\s*)(?:message|enum) \w+ \{`).FindStringIndex(body[i:])
		if m == nil {
			stripped = append(stripped, body[i:]...)
			break
		}
		stripped = append(stripped, body[i:i+m[0]]...)
		depth := 1
		j := i + m[1]
		for depth > 0 && j < len(body) {
			if body[j] == '{' {
				depth++
			}
			if body[j] == '}' {
				depth--
			}
			j++
		}
		i = j
	}
	var out []fieldDesc
	for _, m := range fieldRe.FindAllStringSubmatch(string(stripped), -1) {
		out = append(out, fieldDesc{
			Repeated: strings.Contains(m[1], "repeated"),
			Optional: strings.Contains(m[1], "optional"),
			Type:     m[2],
			Name:     m[3],
			Number:   m[4],
		})
	}
	return out
}

// nestedBlockBody returns the body of `message|enum NAME {` nested
// anywhere inside the given message body (used for Part.MediaResolution
// and its Level enum, and FunctionResponse.Scheduling).
func nestedBlockBody(body, name string) (string, bool) {
	re := regexp.MustCompile(`(?m)^\s*(?:message|enum) ` + regexp.QuoteMeta(name) + ` \{`)
	m := re.FindStringIndex(body)
	if m == nil {
		return "", false
	}
	return braceBody(body[m[1]:]), true
}

// enumBodyValues extracts the value names directly from an enum BODY.
func enumBodyValues(enumBody string) []string {
	var out []string
	for _, line := range strings.Split(enumBody, "\n") {
		mm := regexp.MustCompile(`^\s*(\w+)\s*=\s*\d+\s*;`).FindStringSubmatch(line)
		if mm != nil {
			out = append(out, mm[1])
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// node extraction
// ---------------------------------------------------------------------------

// armNames is the seven Part data arms declared in BOTH pinned surfaces.
var armNames = []string{
	"text", "inline_data", "function_call", "function_response",
	"file_data", "executable_code", "code_execution_result",
}

// wireMember converts a proto snake_case member to its camelCase wire
// spelling (protojson default).
func wireMember(protoName string) string {
	parts := strings.Split(protoName, "_")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

// schemaKind maps a proto field type to the node kind.
func schemaKind(t string, repeated bool) string {
	if repeated {
		return "repeated-" + strings.TrimPrefix(t, "google.protobuf.")
	}
	switch t {
	case "string":
		return "string"
	case "bytes":
		return "bytes"
	case "bool":
		return "bool"
	case "google.protobuf.Struct":
		return "object"
	case "Scheduling", "Level":
		return "enum"
	default:
		return "message"
	}
}

// nodeSurface joins the given surfaces in a stable order.
func nodeSurface(ss ...string) []string {
	var out []string
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// hasSurface reports whether a node with the given surfaces covers s.
func hasSurface(surfaces []string, s string) bool {
	for _, x := range surfaces {
		if x == s {
			return true
		}
	}
	return false
}

// partNodes builds the Part member nodes (arms + ancillaries) for one
// surface.
func partNodes(surface, content string) []SchemaNode {
	var out []SchemaNode
	for _, f := range messageFields(content) {
		isArm := false
		for _, a := range armNames {
			if a == f.Name {
				isArm = true
				break
			}
		}
		kind := schemaKind(f.Type, f.Repeated)
		if isArm {
			out = append(out, SchemaNode{
				ID:     "part.arm." + wireMember(f.Name),
				Member: wireMember(f.Name),
				Kind:   kind,
			})
		} else {
			out = append(out, SchemaNode{
				ID:     "part.ancillary." + wireMember(f.Name),
				Member: wireMember(f.Name),
				Kind:   kind,
			})
		}
	}
	for i := range out {
		out[i].Surfaces = []string{surface}
	}
	return out
}

// mergeNodes unions nodes with the same ID, merging surfaces.
func mergeNodes(nodes []SchemaNode) []SchemaNode {
	byID := map[string]*SchemaNode{}
	var order []string
	for i := range nodes {
		n := nodes[i]
		if _, ok := byID[n.ID]; !ok {
			order = append(order, n.ID)
			byID[n.ID] = &SchemaNode{ID: n.ID, Member: n.Member, Kind: n.Kind}
		}
		dst := byID[n.ID]
		for _, s := range n.Surfaces {
			if !hasSurface(dst.Surfaces, s) {
				dst.Surfaces = append(dst.Surfaces, s)
			}
		}
	}
	sort.Strings(order)
	out := make([]SchemaNode, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out
}

// ParseVendoredSource derives the full schema node set from the vendored
// artifacts (offline; the manifest digests are verified first).
// schedulingArtifactOrder and mediaResolutionLevelArtifactOrder are
// declared in the GENERATED snapshot.gen.go (artifact order); the parser
// only assigns them.

func ParseVendoredSource() ([]SchemaNode, error) {
	m, err := LoadManifest()
	if err != nil {
		return nil, err
	}
	if err := VerifyVendoredDigests(m); err != nil {
		return nil, err
	}
	read := func(local string) (string, error) {
		raw, err := os.ReadFile(filepath.Join("source", local))
		if err != nil {
			return "", err
		}
		return string(raw), nil
	}
	glContent, err := read("generativelanguage-v1beta-content.proto")
	if err != nil {
		return nil, err
	}
	apContent, err := read("aiplatform-v1-content.proto")
	if err != nil {
		return nil, err
	}
	apTool, err := read("aiplatform-v1-tool.proto")
	if err != nil {
		return nil, err
	}

	var nodes []SchemaNode

	// Part members, both surfaces.
	nodes = append(nodes, partNodes(surfaceGemini, extractMessage(glContent, "Part"))...)
	nodes = append(nodes, partNodes(surfaceVertex, extractMessage(apContent, "Part"))...)
	nodes = mergeNodes(nodes)

	// FunctionResponse members per surface.
	funcResp := func(surface, body string) {
		if body == "" {
			return
		}
		for _, f := range messageFields(body) {
			nodes = append(nodes, SchemaNode{
				ID:       "function-response.member." + wireMember(f.Name),
				Member:   wireMember(f.Name),
				Kind:     schemaKind(f.Type, f.Repeated),
				Surfaces: []string{surface},
			})
		}
	}
	funcResp(surfaceGemini, extractMessage(glContent, "FunctionResponse"))
	funcResp(surfaceVertex, extractMessage(apTool, "FunctionResponse"))

	// FunctionResponsePart union arms per surface.
	funcRespPart := func(surface, body string) {
		if body == "" {
			return
		}
		for _, f := range messageFields(body) {
			nodes = append(nodes, SchemaNode{
				ID:       "function-response-part.arm." + wireMember(f.Name),
				Member:   wireMember(f.Name),
				Kind:     schemaKind(f.Type, f.Repeated),
				Surfaces: []string{surface},
			})
		}
	}
	funcRespPart(surfaceGemini, extractMessage(glContent, "FunctionResponsePart"))
	funcRespPart(surfaceVertex, extractMessage(apTool, "FunctionResponsePart"))

	// FunctionResponseBlob / FunctionResponseFileData members.
	blob := func(surface, body string) {
		if body == "" {
			return
		}
		for _, f := range messageFields(body) {
			nodes = append(nodes, SchemaNode{
				ID:       "function-response-blob.member." + wireMember(f.Name),
				Member:   wireMember(f.Name),
				Kind:     schemaKind(f.Type, f.Repeated),
				Surfaces: []string{surface},
			})
		}
	}
	blob(surfaceGemini, extractMessage(glContent, "FunctionResponseBlob"))
	blob(surfaceVertex, extractMessage(apTool, "FunctionResponseBlob"))
	fd := func(surface, body string) {
		if body == "" {
			return
		}
		for _, f := range messageFields(body) {
			nodes = append(nodes, SchemaNode{
				ID:       "function-response-file-data.member." + wireMember(f.Name),
				Member:   wireMember(f.Name),
				Kind:     schemaKind(f.Type, f.Repeated),
				Surfaces: []string{surface},
			})
		}
	}
	fd(surfaceVertex, extractMessage(apTool, "FunctionResponseFileData"))

	// MediaResolution shape (nested in the vertex Part): the level member
	// and its enum vocabulary — the object grammar of the mediaResolution
	// wire value, machine-derived.
	if mrBody, ok := nestedBlockBody(extractMessage(apContent, "Part"), "MediaResolution"); ok {
		for _, f := range messageFields(mrBody) {
			nodes = append(nodes, SchemaNode{
				ID:       "media-resolution.member." + wireMember(f.Name),
				Member:   wireMember(f.Name),
				Kind:     schemaKind(f.Type, f.Repeated),
				Surfaces: []string{surfaceVertex},
			})
		}
		if lv, ok := nestedBlockBody(mrBody, "Level"); ok {
			mediaResolutionLevelArtifactOrder = enumBodyValues(lv)
			for _, v := range mediaResolutionLevelArtifactOrder {
				nodes = append(nodes, SchemaNode{
					ID:       "media-resolution.level.enum." + v,
					Member:   v,
					Kind:     "enum-value",
					Surfaces: []string{surfaceVertex},
				})
			}
		}
	}

	// Scheduling enum values (gemini); artifact order is recorded so the
	// usable vocabulary can be derived in provider order.
	if frBody := extractMessage(glContent, "FunctionResponse"); frBody != "" {
		schBody, ok := nestedBlockBody(frBody, "Scheduling")
		if !ok {
			return nil, fmt.Errorf("Scheduling enum not found in FunctionResponse")
		}
		for _, v := range enumBodyValues(schBody) {
			nodes = append(nodes, SchemaNode{
				ID:       "scheduling.enum." + v,
				Member:   v,
				Kind:     "enum-value",
				Surfaces: []string{surfaceGemini},
			})
		}
		schedulingArtifactOrder = enumBodyValues(schBody)
	}

	return mergeNodes(nodes), nil
}
