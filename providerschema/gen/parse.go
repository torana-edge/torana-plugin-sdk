package gen

// Parsing of the VENDORED provider protos (source/*.proto, pinned by
// source/manifest.json). This is the only place schema facts come from:
// snapshot.gen.go is generated from these bytes, and the offline tests
// re-derive the node set from them — a hand-edited generated table or a
// tampered artifact fails the inventory.
//
// The parser is deliberately small and strict: it handles the constructs
// these three pinned files use (top-level messages, oneof field blocks,
// nested messages/enums, enum values, google.api.field_behavior
// annotations). Missing or unbalanced required messages/enums are a PARSE
// ERROR, never an empty/truncated result; regeneration against a
// materially different upstream file is a reviewed act (generate.sh +
// manifest update).
//
// The contract dimensions the adapter grammar needs are RETAINED on every
// node: oneof membership (arms are derived from the pinned `data` oneof
// blocks, never a hand-written name list), repeated cardinality, proto3
// optional presence, and google.api.field_behavior REQUIRED/OPTIONAL
// (UNSPECIFIED is explicit). Cross-surface merges ERROR on any
// incompatible dimension instead of taking the first definition.

import (
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
	SurfaceGemini = "gemini"
	SurfaceVertex = "vertex"
)

// FieldBehavior is the google.api.field_behavior annotation (UNSPECIFIED
// is explicit: the field carries no annotation).
type FieldBehavior string

const (
	BehaviorUnspecified FieldBehavior = "UNSPECIFIED"
	BehaviorRequired    FieldBehavior = "REQUIRED"
	BehaviorOptional    FieldBehavior = "OPTIONAL"
)

// SchemaNode is one machine-derived schema fact from the vendored protos.
// ID is a namespaced identity; Member is the camelCase wire spelling; Kind
// is the wire value kind; Surfaces names the surfaces declaring it; Oneof
// names the oneof block the member belongs to ("" for direct fields);
// Repeated/Optional/FieldBehavior are the retained contract dimensions.
// Enum VALUES are their own nodes (scheduling.enum.*, media-resolution.
// level.enum.*) so the vocabulary is part of the bidirectional inventory.
type SchemaNode struct {
	ID            string
	Member        string
	Kind          string
	Surfaces      []string
	Oneof         string
	Repeated      bool
	Optional      bool
	FieldBehavior FieldBehavior
}

// EnumOrders carries the provider enum orders in ARTIFACT order (the
// generated file declares them; the parser returns them explicitly rather
// than mutating generated globals — the generator bootstraps without the
// old output).
type EnumOrders struct {
	Scheduling           []string
	MediaResolutionLevel []string
}

// ---------------------------------------------------------------------------
// manifest + vendored artifact access
// ---------------------------------------------------------------------------

// LoadManifestFrom reads + validates a manifest at an explicit path
// (the bootstrap test uses a temp copy).
func LoadManifestFrom(path string) (*Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read vendored manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse vendored manifest: %w", err)
	}
	if err := validateManifest(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

// ---------------------------------------------------------------------------
// proto text parsing (constrained to the pinned artifacts)
// ---------------------------------------------------------------------------

var oneofRe = regexp.MustCompile(`(?m)^\s*oneof (\w+) \{`)

// extractMessage returns the BODY of the first top-level `message NAME {`
// in s. Missing and unbalanced inputs are errors, never empty results.
func extractMessage(s, name string) (string, bool, error) {
	re := regexp.MustCompile(`(?m)^message ` + regexp.QuoteMeta(name) + ` \{`)
	m := re.FindStringIndex(s)
	if m == nil {
		return "", false, nil
	}
	body, err := braceBody(s[m[0]+len("message ")+len(name)+2:])
	if err != nil {
		return "", false, fmt.Errorf("message %s: %w", name, err)
	}
	return body, true, nil
}

// braceBody consumes a balanced `{ ... }` starting at the text immediately
// after the opening brace and returns the body; unbalanced input errors.
func braceBody(s string) (string, error) {
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
	if depth != 0 {
		return "", fmt.Errorf("unbalanced braces in proto text")
	}
	return s[:i-1], nil
}

// fieldDesc is one parsed field with its retained contract dimensions.
type fieldDesc struct {
	Repeated      bool
	Optional      bool
	Type          string
	Name          string
	Number        string
	Oneof         string
	FieldBehavior FieldBehavior
}

var fieldRe = regexp.MustCompile(`(?m)^\s*(repeated\s+|optional\s+)?([\w.]+)\s+(\w+)\s*=\s*(\d+)\s*(\[[^\]]*\])?\s*;`)

var behaviorRe = regexp.MustCompile(`google\.api\.field_behavior\)\s*=\s*(\w+)`)

// messageFields returns the fields declared directly in a message body,
// INCLUDING fields inside oneof blocks (with their oneof name), EXCLUDING
// fields of nested messages/enums. Oneof grouping is a schema fact: arms
// are classified by their oneof membership, not a name list.
func messageFields(body string) ([]fieldDesc, error) {
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
		if depth != 0 {
			return nil, fmt.Errorf("unbalanced nested block")
		}
		i = j
	}
	text := string(stripped)

	// Oneof intervals: each `oneof NAME {` block's [start, end) offset
	// range (oneofs cannot nest, so a plain interval list is exact). A
	// field's oneof membership is its containing interval — arms are
	// classified by the schema source, never a name list.
	type oneofInterval struct {
		name  string
		start int // offset of the opening '{'
		end   int // offset just past the closing '}'
	}
	var oneofs []oneofInterval
	for _, m := range oneofRe.FindAllStringSubmatchIndex(text, -1) {
		open := m[1] // end of the name match; the '{' is here
		name := text[m[2]:m[3]]
		depth := 1
		j := open
		for depth > 0 && j < len(text) {
			if text[j] == '{' {
				depth++
			}
			if text[j] == '}' {
				depth--
			}
			j++
		}
		if depth != 0 {
			return nil, fmt.Errorf("unbalanced oneof block")
		}
		oneofs = append(oneofs, oneofInterval{name: name, start: open, end: j})
	}
	oneofAt := func(offset int) string {
		for _, o := range oneofs {
			if offset >= o.start && offset < o.end {
				return o.name
			}
		}
		return ""
	}

	var out []fieldDesc
	for _, m := range fieldRe.FindAllStringSubmatchIndex(text, -1) {
		fb := BehaviorUnspecified
		optStart, optEnd := m[10], m[11]
		if optStart >= 0 {
			if b := behaviorRe.FindStringSubmatch(text[optStart:optEnd]); b != nil {
				switch b[1] {
				case "REQUIRED":
					fb = BehaviorRequired
				case "OPTIONAL":
					fb = BehaviorOptional
				}
			}
		}
		prefix := ""
		if m[2] >= 0 {
			prefix = text[m[2]:m[3]]
		}
		out = append(out, fieldDesc{
			Repeated:      strings.Contains(prefix, "repeated"),
			Optional:      strings.Contains(prefix, "optional"),
			Type:          text[m[4]:m[5]],
			Name:          text[m[6]:m[7]],
			Number:        text[m[8]:m[9]],
			Oneof:         oneofAt(m[0]),
			FieldBehavior: fb,
		})
	}
	return out, nil
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

// nestedBlockBody returns the body of `message|enum NAME {` nested
// anywhere inside the given message body; unbalanced input errors.
func nestedBlockBody(body, name string) (string, bool, error) {
	re := regexp.MustCompile(`(?m)^\s*(?:message|enum) ` + regexp.QuoteMeta(name) + ` \{`)
	m := re.FindStringIndex(body)
	if m == nil {
		return "", false, nil
	}
	b, err := braceBody(body[m[1]:])
	if err != nil {
		return "", false, fmt.Errorf("nested %s: %w", name, err)
	}
	return b, true, nil
}

// ---------------------------------------------------------------------------
// node extraction
// ---------------------------------------------------------------------------

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

// dataOneof is the pinned oneof whose members are Part data arms.
const dataOneof = "data"

// partNodes builds the Part member nodes for one surface. Arms are the
// members of the pinned `data` oneof — the SCHEMA SOURCE classifies them,
// never a hand-written name list; every other direct field (including the
// `metadata` oneof members) is an ancillary.
func partNodes(surface, content string) ([]SchemaNode, error) {
	fields, err := messageFields(content)
	if err != nil {
		return nil, fmt.Errorf("surface %s Part: %w", surface, err)
	}
	var out []SchemaNode
	for _, f := range fields {
		id := "part.ancillary." + wireMember(f.Name)
		if f.Oneof == dataOneof {
			id = "part.arm." + wireMember(f.Name)
		}
		out = append(out, SchemaNode{
			ID:            id,
			Member:        wireMember(f.Name),
			Kind:          schemaKind(f.Type, f.Repeated),
			Surfaces:      []string{surface},
			Oneof:         f.Oneof,
			Repeated:      f.Repeated,
			Optional:      f.Optional,
			FieldBehavior: f.FieldBehavior,
		})
	}
	return out, nil
}

// memberNodes builds the nodes for a message whose members are all
// "members" (FunctionResponse) or whose oneof-data members are arms
// (FunctionResponsePart).
func memberNodes(idPrefix string, armOneof bool, surface, body string) ([]SchemaNode, error) {
	fields, err := messageFields(body)
	if err != nil {
		return nil, fmt.Errorf("surface %s %s: %w", surface, idPrefix, err)
	}
	var out []SchemaNode
	for _, f := range fields {
		isArm := armOneof && f.Oneof == dataOneof
		kind := schemaKind(f.Type, f.Repeated)
		if isArm {
			out = append(out, SchemaNode{
				ID:            idPrefix + ".arm." + wireMember(f.Name),
				Member:        wireMember(f.Name),
				Kind:          kind,
				Oneof:         f.Oneof,
				Repeated:      f.Repeated,
				Optional:      f.Optional,
				FieldBehavior: f.FieldBehavior,
			})
		} else {
			out = append(out, SchemaNode{
				ID:            idPrefix + ".member." + wireMember(f.Name),
				Member:        wireMember(f.Name),
				Kind:          kind,
				Oneof:         f.Oneof,
				Repeated:      f.Repeated,
				Optional:      f.Optional,
				FieldBehavior: f.FieldBehavior,
			})
		}
	}
	for i := range out {
		out[i].Surfaces = []string{surface}
	}
	return out, nil
}

// mergeNodes unions nodes with the same ID, merging surfaces. ANY
// incompatible dimension (member, kind, cardinality, proto-optional
// presence, oneof role, or requiredness) is an ERROR — the first
// definition is never silently kept. UNSPECIFIED behavior merges with
// OPTIONAL to OPTIONAL (the more specific annotation); REQUIRED conflicts
// with both.
func mergeNodes(nodes []SchemaNode) ([]SchemaNode, error) {
	byID := map[string]*SchemaNode{}
	var order []string
	for i := range nodes {
		n := nodes[i]
		prev, ok := byID[n.ID]
		if !ok {
			cp := n
			byID[n.ID] = &cp
			order = append(order, n.ID)
			continue
		}
		if prev.Member != n.Member {
			return nil, fmt.Errorf("schema conflict %s: member %q vs %q", n.ID, prev.Member, n.Member)
		}
		if prev.Kind != n.Kind {
			return nil, fmt.Errorf("schema conflict %s: kind %q vs %q", n.ID, prev.Kind, n.Kind)
		}
		if prev.Repeated != n.Repeated {
			return nil, fmt.Errorf("schema conflict %s: repeated %v vs %v", n.ID, prev.Repeated, n.Repeated)
		}
		if prev.Optional != n.Optional {
			return nil, fmt.Errorf("schema conflict %s: proto-optional %v vs %v", n.ID, prev.Optional, n.Optional)
		}
		if prev.Oneof != n.Oneof {
			return nil, fmt.Errorf("schema conflict %s: oneof %q vs %q", n.ID, prev.Oneof, n.Oneof)
		}
		// Requiredness: an explicit contradiction (REQUIRED vs OPTIONAL)
		// is a schema conflict; UNSPECIFIED (no annotation on that
		// surface) absorbs the other surface's more specific annotation
		// (REQUIRED wins over UNSPECIFIED, OPTIONAL wins over
		// UNSPECIFIED). A single-surface REQUIRED->OPTIONAL change is
		// therefore visible in the merged node whenever the other surface
		// does not also require it.
		if (prev.FieldBehavior == BehaviorRequired && n.FieldBehavior == BehaviorOptional) ||
			(prev.FieldBehavior == BehaviorOptional && n.FieldBehavior == BehaviorRequired) {
			return nil, fmt.Errorf("schema conflict %s: field_behavior %s vs %s", n.ID, prev.FieldBehavior, n.FieldBehavior)
		}
		if prev.FieldBehavior == BehaviorUnspecified {
			prev.FieldBehavior = n.FieldBehavior
		}
		for _, s := range n.Surfaces {
			if !hasSurface(prev.Surfaces, s) {
				prev.Surfaces = append(prev.Surfaces, s)
			}
		}
	}
	sort.Strings(order)
	out := make([]SchemaNode, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out, nil
}

func hasSurface(surfaces []string, s string) bool {
	for _, x := range surfaces {
		if x == s {
			return true
		}
	}
	return false
}

// ParseVendoredSource derives the complete schema node set and enum
// orders from the vendored artifacts (offline; the manifest is validated
// and the digests verified first). Every required message/enum must be
// present and balanced — a missing or truncated schema is an error.
func ParseVendoredSource() ([]SchemaNode, *EnumOrders, error) {
	m, err := LoadManifest()
	if err != nil {
		return nil, nil, err
	}
	if err := VerifyVendoredDigests(m); err != nil {
		return nil, nil, err
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
		return nil, nil, err
	}
	apContent, err := read("aiplatform-v1-content.proto")
	if err != nil {
		return nil, nil, err
	}
	apTool, err := read("aiplatform-v1-tool.proto")
	if err != nil {
		return nil, nil, err
	}

	var nodes []SchemaNode
	orders := &EnumOrders{}

	// Part members, both surfaces (arms derived from the `data` oneof).
	glPart, ok, err := extractMessage(glContent, "Part")
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, fmt.Errorf("generativelanguage content.proto: message Part missing")
	}
	apPart, ok, err := extractMessage(apContent, "Part")
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, fmt.Errorf("aiplatform content.proto: message Part missing")
	}
	glPartNodes, err := partNodes(SurfaceGemini, glPart)
	if err != nil {
		return nil, nil, err
	}
	apPartNodes, err := partNodes(SurfaceVertex, apPart)
	if err != nil {
		return nil, nil, err
	}
	nodes = append(nodes, glPartNodes...)
	nodes = append(nodes, apPartNodes...)

	// FunctionResponse members per surface.
	glFR, ok, err := extractMessage(glContent, "FunctionResponse")
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, fmt.Errorf("generativelanguage content.proto: message FunctionResponse missing")
	}
	apFR, ok, err := extractMessage(apTool, "FunctionResponse")
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, fmt.Errorf("aiplatform tool.proto: message FunctionResponse missing")
	}
	frNodes, err := memberNodes("function-response", false, SurfaceGemini, glFR)
	if err != nil {
		return nil, nil, err
	}
	nodes = append(nodes, frNodes...)
	frNodes, err = memberNodes("function-response", false, SurfaceVertex, apFR)
	if err != nil {
		return nil, nil, err
	}
	nodes = append(nodes, frNodes...)

	// FunctionResponsePart union arms per surface (sealed `data` oneof).
	glFRP, ok, err := extractMessage(glContent, "FunctionResponsePart")
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, fmt.Errorf("generativelanguage content.proto: message FunctionResponsePart missing")
	}
	apFRP, ok, err := extractMessage(apTool, "FunctionResponsePart")
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, fmt.Errorf("aiplatform tool.proto: message FunctionResponsePart missing")
	}
	frpNodes, err := memberNodes("function-response-part", true, SurfaceGemini, glFRP)
	if err != nil {
		return nil, nil, err
	}
	nodes = append(nodes, frpNodes...)
	frpNodes, err = memberNodes("function-response-part", true, SurfaceVertex, apFRP)
	if err != nil {
		return nil, nil, err
	}
	nodes = append(nodes, frpNodes...)

	// FunctionResponseBlob / FunctionResponseFileData members.
	glBlob, ok, err := extractMessage(glContent, "FunctionResponseBlob")
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, fmt.Errorf("generativelanguage content.proto: message FunctionResponseBlob missing")
	}
	apBlob, ok, err := extractMessage(apTool, "FunctionResponseBlob")
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, fmt.Errorf("aiplatform tool.proto: message FunctionResponseBlob missing")
	}
	blobNodes, err := memberNodes("function-response-blob", false, SurfaceGemini, glBlob)
	if err != nil {
		return nil, nil, err
	}
	nodes = append(nodes, blobNodes...)
	blobNodes, err = memberNodes("function-response-blob", false, SurfaceVertex, apBlob)
	if err != nil {
		return nil, nil, err
	}
	nodes = append(nodes, blobNodes...)
	apFD, ok, err := extractMessage(apTool, "FunctionResponseFileData")
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, fmt.Errorf("aiplatform tool.proto: message FunctionResponseFileData missing")
	}
	fdNodes, err := memberNodes("function-response-file-data", false, SurfaceVertex, apFD)
	if err != nil {
		return nil, nil, err
	}
	nodes = append(nodes, fdNodes...)

	// MediaResolution shape (nested in the vertex Part): the level member
	// and its enum vocabulary — the object grammar of the mediaResolution
	// wire value, machine-derived.
	mrBody, ok, err := nestedBlockBody(apPart, "MediaResolution")
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, fmt.Errorf("aiplatform content.proto: nested MediaResolution missing")
	}
	mrNodes, err := memberNodes("media-resolution", false, SurfaceVertex, mrBody)
	if err != nil {
		return nil, nil, err
	}
	nodes = append(nodes, mrNodes...)
	levBody, ok, err := nestedBlockBody(mrBody, "Level")
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, fmt.Errorf("aiplatform content.proto: nested Level enum missing")
	}
	orders.MediaResolutionLevel = enumBodyValues(levBody)
	for _, v := range orders.MediaResolutionLevel {
		nodes = append(nodes, SchemaNode{
			ID:       "media-resolution.level.enum." + v,
			Member:   v,
			Kind:     "enum-value",
			Surfaces: []string{SurfaceVertex},
		})
	}

	// Scheduling enum values (gemini); artifact order recorded.
	schBody, ok, err := nestedBlockBody(glFR, "Scheduling")
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, fmt.Errorf("generativelanguage content.proto: nested Scheduling enum missing")
	}
	orders.Scheduling = enumBodyValues(schBody)
	for _, v := range orders.Scheduling {
		nodes = append(nodes, SchemaNode{
			ID:       "scheduling.enum." + v,
			Member:   v,
			Kind:     "enum-value",
			Surfaces: []string{SurfaceGemini},
		})
	}

	merged, err := mergeNodes(nodes)
	if err != nil {
		return nil, nil, err
	}
	return merged, orders, nil
}

// SyntheticPartArms parses a full synthetic Part proto text and returns
// the set of classified arm IDs. Used by the agent-arm absence guard test
// to prove the guard trips when a real `tool_call` member appears in a
// Part's data oneof.
func SyntheticPartArms(full string) (map[string]bool, error) {
	body, ok, err := extractMessage(full, "Part")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("synthetic Part missing")
	}
	nodes, err := partNodes(SurfaceGemini, body)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, n := range nodes {
		out[n.ID] = true
	}
	return out, nil
}
