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
	m, err := decodeManifest(raw)
	if err != nil {
		return nil, err
	}
	if err := validateManifest(m); err != nil {
		return nil, err
	}
	return m, nil
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
	JSONName      string // explicit json_name wire spelling ("" = derive)
}

// Field declaration line head: [repeated|optional] TYPE name = NUMBER
// (the tail — `;` or a bracketed option list, possibly wrapped — is
// validated by the strict line scanner, not this regex).
var fieldHeadRe = regexp.MustCompile(`^\s*(repeated\s+|optional\s+)?([\w.]+)\s+(\w+)\s*=\s*(-?\d+)\s*`)

// Map field declaration: map<KEY, VALUE> name = NUMBER.
var mapFieldRe = regexp.MustCompile(`^\s*map<\s*([\w.]+)\s*,\s*([\w.]+)\s*>\s+(\w+)\s*=\s*(-?\d+)\s*`)

// Option continuation line (wrapped field options end with `;`).
var optionLineRe = regexp.MustCompile(`^\s*(\[[^\]]*\])\s*;?\s*$`)

// closeLineRe is the FULL-LINE oneof/message close rule: a line that is
// not exactly `}` (plus optional trailing comment) is NOT a close — a
// `} invented_field = 9;` line must error, never silently drop its tail.
var closeLineRe = regexp.MustCompile(`^\s*}\s*(//.*)?$`)

var behaviorRe = regexp.MustCompile(`google\.api\.field_behavior\)\s*=\s*(\w+)`)

// jsonNamePresenceRe detects ANY json_name mention; the VALUE is then
// validated separately so empty/duplicate/malformed spellings can never
// fall back to the derived name.
var jsonNamePresenceRe = regexp.MustCompile(`json_name`)
var jsonNameRe = regexp.MustCompile(`json_name\s*=\s*"([^"]*)"`)

// commentLineRe matches blank/comment lines (skipped by the accounting).
var commentLineRe = regexp.MustCompile(`^\s*(//.*)?$`)

// parseFieldOptions validates the option text affecting the retained
// contract dimensions and returns the field behavior + explicit json_name.
// Unsupported field-behavior values are a STABLE ERROR, never a silent
// collapse to UNSPECIFIED.
func parseFieldOptions(opt string) (FieldBehavior, string, error) {
	fb := BehaviorUnspecified
	behaviors := behaviorRe.FindAllStringSubmatch(opt, -1)
	for _, b := range behaviors {
		switch b[1] {
		case "REQUIRED":
			if fb != BehaviorUnspecified && fb != BehaviorRequired {
				return "", "", fmt.Errorf("conflicting field_behavior annotations: %s", opt)
			}
			fb = BehaviorRequired
		case "OPTIONAL":
			if fb != BehaviorUnspecified && fb != BehaviorOptional {
				return "", "", fmt.Errorf("conflicting field_behavior annotations: %s", opt)
			}
			fb = BehaviorOptional
		default:
			return "", "", fmt.Errorf("unsupported google.api.field_behavior %q in options %q (only REQUIRED/OPTIONAL are supported)", b[1], opt)
		}
	}
	jsonName := ""
	mentions := jsonNamePresenceRe.FindAllStringIndex(opt, -1)
	if len(mentions) > 1 {
		return "", "", fmt.Errorf("duplicate json_name in options %q", opt)
	}
	if len(mentions) == 1 {
		m := jsonNameRe.FindStringSubmatch(opt)
		if m == nil {
			return "", "", fmt.Errorf("malformed json_name in options %q (expected json_name = \"...\")", opt)
		}
		if m[1] == "" {
			return "", "", fmt.Errorf("empty json_name in options %q", opt)
		}
		jsonName = m[1]
	}
	return fb, jsonName, nil
}

// messageFields returns the fields declared directly in a message body,
// INCLUDING fields inside oneof blocks (with their oneof name), EXCLUDING
// fields of nested messages/enums.
//
// FAIL-CLOSED ACCOUNTING: every non-comment line of the body is consumed
// by exactly one rule (field head, map field, oneof open/close, wrapped
// option continuation). An unrecognized construct — a new provider
// declaration form the parser does not understand — is a STABLE ERROR,
// never a silent omission; a declaration can never disappear from the
// inventory.
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
	// range (oneofs cannot nest, so a plain interval list is exact).
	type oneofInterval struct {
		name  string
		start int
		end   int
	}
	var oneofs []oneofInterval
	for _, m := range oneofRe.FindAllStringSubmatchIndex(text, -1) {
		open := m[1]
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

	// Strict line accounting: every non-comment line must be consumed.
	var out []fieldDesc
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if commentLineRe.MatchString(line) {
			continue
		}
		if m := oneofRe.FindStringSubmatch(line); m != nil {
			continue // handled by the interval pass
		}
		if closeLineRe.MatchString(line) {
			continue // full-line oneof/message close (tail must be a comment)
		}
		// Wrapped-option continuation from the previous field line.
		if optionLineRe.MatchString(line) {
			// Consumed by the field tail below; a lone continuation with
			// no pending field is an error.
			return nil, fmt.Errorf("orphan option continuation line %q", strings.TrimSpace(line))
		}

		// Field declarations: map first (its type token contains commas
		// the plain regex cannot parse), then plain fields.
		if m := mapFieldRe.FindStringSubmatch(line); m != nil {
			// Maps cannot carry repeated/optional prefixes; the prefix is "".
			tail := line[len(m[0]):]
			f, consumed, err := parseFieldLine(line, tail, lines[i+1:], m[3], m[4], "map<"+m[1]+","+m[2]+">", "")
			if err != nil {
				return nil, err
			}
			i += consumed
			f.Oneof = oneofAt(lineOffset(text, lines, i-consumed))
			out = append(out, f)
			continue
		}
		if m := fieldHeadRe.FindStringSubmatch(line); m != nil {
			tail := line[len(m[0]):]
			f, consumed, err := parseFieldLine(line, tail, lines[i+1:], m[3], m[4], m[2], m[1])
			if err != nil {
				return nil, err
			}
			i += consumed
			f.Oneof = oneofAt(lineOffset(text, lines, i-consumed))
			out = append(out, f)
			continue
		}
		return nil, fmt.Errorf("unrecognized construct in message body: %q", strings.TrimSpace(line))
	}
	return out, nil
}

// lineOffset returns the byte offset of lines[idx] within text.
func lineOffset(text string, lines []string, idx int) int {
	if idx < 0 || idx >= len(lines) {
		return len(text)
	}
	off := 0
	for i := 0; i < idx; i++ {
		off += len(lines[i]) + 1
	}
	if off > len(text) {
		return len(text)
	}
	return off
}

// parseFieldLine parses one field declaration: line is the head line
// (`[repeated|optional] TYPE name = NUMBER`), rest are the following
// lines (consumed when the option list is wrapped). prefix carries the
// repeated/optional token; typ the proto type; name the proto member;
// number the field number. The tail after the number must be `;`, a
// bracketed option list, or a wrapped option continuation — anything
// else is a stable error.
func parseFieldLine(line string, tail string, rest []string, name, number, typ, prefix string) (fieldDesc, int, error) {
	f := fieldDesc{
		Repeated:      strings.Contains(prefix, "repeated"),
		Optional:      strings.Contains(prefix, "optional"),
		Type:          typ,
		Name:          name,
		Number:        number,
		FieldBehavior: BehaviorUnspecified,
	}
	_ = line
	consumed := 0
	optText := ""
	restOf := strings.TrimSpace(tail)
	switch {
	case restOf == "" || restOf == ";" || isCommentTail(restOf):
		// No options on the head line — but a WRAPPED option may begin on
		// the next line (`= 6\n    [(google.api.field_behavior) = ...];`).
		if len(rest) > 0 && optionLineRe.MatchString(strings.TrimSpace(rest[0])) {
			c := strings.TrimSpace(rest[0])
			consumed = 1
			optText = c
			if !strings.HasSuffix(c, ";") {
				return fieldDesc{}, 0, fmt.Errorf("field %s: wrapped option not terminated on one line", name)
			}
		}
	case strings.HasPrefix(restOf, "["):
		// Structural completeness: the option must be a BALANCED bracket
		// group ending with `];` (a trailing comment after `;` is fine).
		if !isCompleteOptionTail(restOf) {
			return fieldDesc{}, 0, fmt.Errorf("field %s: structurally incomplete option tail %q", name, restOf)
		}
		optText = restOf
		if !strings.HasSuffix(strings.TrimSpace(optText), ";") {
			// Wrapped option list: consume continuation lines until one
			// ends with ';'.
			for consumed < len(rest) {
				c := strings.TrimSpace(rest[consumed])
				consumed++
				if !optionLineRe.MatchString(c) {
					return fieldDesc{}, 0, fmt.Errorf("field %s: malformed wrapped option %q", name, c)
				}
				optText += c
				if strings.HasSuffix(c, ";") {
					break
				}
			}
			if !strings.HasSuffix(strings.TrimSpace(optText), ";") {
				return fieldDesc{}, 0, fmt.Errorf("field %s: unterminated wrapped option", name)
			}
		}
	default:
		return fieldDesc{}, 0, fmt.Errorf("field %s: unrecognized construct after the number: %q", name, restOf)
	}
	if optText != "" {
		fb, jsonName, err := parseFieldOptions(optText)
		if err != nil {
			return fieldDesc{}, 0, fmt.Errorf("field %s: %w", name, err)
		}
		f.FieldBehavior = fb
		if jsonName != "" {
			f.JSONName = jsonName
		}
	}
	return f, consumed, nil
}

// enumBodyValues extracts the value names directly from an enum BODY.
// enumValueRe matches a value declaration, including negative numbers
// and trailing options.
var enumValueRe = regexp.MustCompile(`^\s*(\w+)\s*=\s*(-?\d+)\s*(\[[^\]]*\])?\s*;\s*$`)

// enumBodyValues extracts the value names directly from an enum BODY with
// FAIL-CLOSED ACCOUNTING: every non-comment line must be a value
// declaration (positive or negative number), else a stable error — a
// syntactically valid but unsupported enum form can never disappear.
func enumBodyValues(enumBody string) ([]string, error) {
	var out []string
	for _, line := range strings.Split(enumBody, "\n") {
		if commentLineRe.MatchString(line) {
			continue
		}
		mm := enumValueRe.FindStringSubmatch(line)
		if mm == nil {
			return nil, fmt.Errorf("unrecognized construct in enum body: %q", strings.TrimSpace(line))
		}
		out = append(out, mm[1])
	}
	return out, nil
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

// fieldMember is the wire member of a parsed field: the explicit
// json_name when declared, else the derived camelCase spelling. An
// explicit json_name is NEVER silently substituted by the default.
func fieldMember(f fieldDesc) string {
	if f.JSONName != "" {
		return f.JSONName
	}
	return wireMember(f.Name)
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
	if strings.HasPrefix(t, "map<") {
		return "map"
	}
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
		id := "part.ancillary." + fieldMember(f)
		if f.Oneof == dataOneof {
			id = "part.arm." + fieldMember(f)
		}
		out = append(out, SchemaNode{
			ID:            id,
			Member:        fieldMember(f),
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
		prefix := ".member."
		if isArm {
			prefix = ".arm."
		}
		out = append(out, SchemaNode{
			ID:            idPrefix + prefix + fieldMember(f),
			Member:        fieldMember(f),
			Kind:          kind,
			Surfaces:      []string{surface},
			Oneof:         f.Oneof,
			Repeated:      f.Repeated,
			Optional:      f.Optional,
			FieldBehavior: f.FieldBehavior,
		})
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
	orders.MediaResolutionLevel, err = enumBodyValues(levBody)
	if err != nil {
		return nil, nil, fmt.Errorf("aiplatform content.proto Level enum: %w", err)
	}
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
	orders.Scheduling, err = enumBodyValues(schBody)
	if err != nil {
		return nil, nil, fmt.Errorf("generativelanguage content.proto Scheduling enum: %w", err)
	}
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

// isCompleteOptionTail checks that an inline option tail is structurally
// complete: a BALANCED bracket group ending with `];` (a trailing comment
// is allowed). An unclosed `[json_name = "x";` or a missing `;` fails.
func isCompleteOptionTail(tail string) bool {
	t := strings.TrimSpace(tail)
	// Strip a trailing comment first.
	if i := strings.Index(t, "//"); i >= 0 {
		t = strings.TrimSpace(t[:i])
	}
	if !strings.HasSuffix(t, ";") {
		return false
	}
	t = strings.TrimSpace(t[:len(t)-1])
	if !strings.HasPrefix(t, "[") || !strings.HasSuffix(t, "]") {
		return false
	}
	return strings.Count(t, "[") == 1 && strings.Count(t, "]") == 1
}

// isCommentTail reports a tail that is only whitespace + a comment.
func isCommentTail(tail string) bool {
	return commentLineRe.MatchString(strings.TrimSpace(tail))
}
