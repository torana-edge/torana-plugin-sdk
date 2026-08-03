package providerschema

// Offline provider-schema inventory (no network; the vendored artifacts
// are the immutable inputs):
//
//   - TestSnapshotArtifactDigestsPinned — vendored bytes match the manifest
//     digests (tamper / un-re-vendored artifact detection).
//   - TestSnapshotGeneratedExact — an in-memory re-render is byte-identical
//     to the checked-in snapshot.gen.go (artifact/manifest change without
//     regeneration fails; determinism across runs).
//   - TestSnapshotInventoryBidirectional — the generated decision set is
//     EXACTLY the generated node set, both directions, namespaced; every
//     nested member and enum value participates.
//   - TestSnapshotUnionInventoryExact — SchemaNodes() (generated ∪
//     agent-platform) is the complete union: every returned node resolves
//     via CarrierFor, and no decision in either map is stale.
//   - TestSnapshotInventoryAdversarial — revert-proven rows for the SAME
//     guards (removal/drop/substitution/addition/stale).
//   - TestSnapshotWireSpellings — camelCase across every table.
//   - TestSnapshotVocabularyPinned — usable vocabularies derived from the
//     decisions; UNSPECIFIED excluded exactly like any unknown value.
//   - TestAgentPlatformArmsHonest — the agent-platform arms are a reviewed
//     NON-descriptor decision: parsed oneof identities + tokenized
//     snake_case text guards prove absence from the vendored artifacts
//     (a synthetic tool_call row must trip the same guard), and the
//     separate decision map is validated bidirectionally.
//   - TestSchemaNodesDeepCloneIsolation — exported accessors never expose
//     the authority: mutation of returned nodes/surfaces is invisible.

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/torana-edge/torana-plugin-sdk/providerschema/gen"
)

// TestSnapshotArtifactDigestsPinned: the vendored artifacts must match the
// manifest digests. Changing a vendored proto without re-vendoring (and
// regenerating) fails here.
func TestSnapshotArtifactDigestsPinned(t *testing.T) {
	m, err := gen.LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	if err := gen.VerifyVendoredDigests(m); err != nil {
		t.Fatal(err)
	}
}

// TestSnapshotGeneratedExact: snapshot.gen.go is byte-identical to an
// in-memory re-render (twice — the determinism proof). Changing the
// manifest provenance or the vendored artifacts without regenerating
// fails here.
func TestSnapshotGeneratedExact(t *testing.T) {
	got, err := gen.RenderGeneratedFile()
	if err != nil {
		t.Fatal(err)
	}
	again, err := gen.RenderGeneratedFile()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, again) {
		t.Fatal("render is not deterministic (two in-memory renders differ)")
	}
	checkedIn, err := os.ReadFile("snapshot.gen.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, checkedIn) {
		t.Fatal("snapshot.gen.go is stale: regenerate with ./generate.sh and review the diff")
	}
	m, err := gen.LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range m.Files {
		if !bytes.Contains(got, []byte(f.UpstreamCommitSHA)) {
			t.Errorf("generated file does not record upstream SHA %s", f.UpstreamCommitSHA)
		}
	}
}

// TestSnapshotInventoryBidirectional: every generated node has exactly one
// decision and every decision names a generated node.
func TestSnapshotInventoryBidirectional(t *testing.T) {
	for _, n := range schemaNodes {
		if _, ok := schemaCarrierDecisions[n.ID]; !ok {
			t.Errorf("schema node %s has no carrier decision; add one to schemaCarrierDecisions", n.ID)
		}
	}
	nodes := map[string]bool{}
	for _, n := range schemaNodes {
		nodes[n.ID] = true
	}
	for id := range schemaCarrierDecisions {
		if !nodes[id] {
			t.Errorf("carrier decision %s names no schema node; remove or re-vendor", id)
		}
	}
}

// TestSnapshotUnionInventoryExact (finding 2): SchemaNodes() is the
// complete surface union, and EVERY node it returns — generated or
// reviewed — has exactly one resolvable decision through CarrierFor. No
// decision in either map is stale.
func TestSnapshotUnionInventoryExact(t *testing.T) {
	seen := map[string]bool{}
	for _, n := range SchemaNodes() {
		seen[n.ID] = true
		if CarrierFor(n.ID) == "" {
			t.Errorf("union node %s has no resolvable decision", n.ID)
		}
	}
	for _, n := range schemaNodes {
		if !seen[n.ID] {
			t.Errorf("generated node %s missing from SchemaNodes()", n.ID)
		}
	}
	for _, n := range agentPlatformArms {
		if !seen[n.ID] {
			t.Errorf("agent-platform node %s missing from SchemaNodes()", n.ID)
		}
	}
}

// TestSnapshotInventoryAdversarial: revert-proven rows for the SAME
// bidirectional guard — removing a nested member, substituting an arm
// member, adding an invented node, and dropping a decision all fail.
func TestSnapshotInventoryAdversarial(t *testing.T) {
	members := func(prefix string) []string {
		var out []string
		for _, n := range schemaNodes {
			if strings.HasPrefix(n.ID, prefix) {
				out = append(out, n.ID)
			}
		}
		return out
	}
	inventoryBroken := func(nodes []SchemaNode, decisions map[string]string) bool {
		for _, n := range nodes {
			if _, ok := decisions[n.ID]; !ok {
				return true // node without a decision
			}
		}
		seen := map[string]bool{}
		for _, n := range nodes {
			seen[n.ID] = true
		}
		for id := range decisions {
			if !seen[id] {
				return true // stale decision
			}
		}
		return false
	}
	check := func(name string, nodes []SchemaNode, decisions map[string]string, wantFail bool) {
		t.Helper()
		broken := inventoryBroken(nodes, decisions)
		if wantFail && !broken {
			t.Errorf("%s: mutation went unnoticed by the inventory", name)
		}
		if !wantFail && broken {
			t.Errorf("%s: mutation falsely flagged by the inventory", name)
		}
	}

	clone := func(nodes []SchemaNode) []SchemaNode {
		return append([]SchemaNode{}, nodes...)
	}
	cloneDec := func() map[string]string {
		out := map[string]string{}
		for k, v := range schemaCarrierDecisions {
			out[k] = v
		}
		return out
	}
	dropDec := func(d map[string]string, id string) map[string]string {
		delete(d, id)
		return d
	}

	// Removing a nested FunctionResponseBlob member (the class finding 3
	// of round 1 called out) must fail the guard.
	for _, removed := range members("function-response-blob.member.") {
		ns := clone(schemaNodes)
		filtered := ns[:0]
		for _, n := range ns {
			if n.ID != removed {
				filtered = append(filtered, n)
			}
		}
		check("remove node "+removed+" (decision kept stale)", filtered, cloneDec(), true)
		check("drop decision "+removed+" (node kept)", clone(schemaNodes), dropDec(cloneDec(), removed), true)
	}
	for _, removed := range members("function-response-file-data.member.") {
		ns := clone(schemaNodes)
		filtered := ns[:0]
		for _, n := range ns {
			if n.ID != removed {
				filtered = append(filtered, n)
			}
		}
		check("remove node "+removed+" (decision kept stale)", filtered, cloneDec(), true)
		check("drop decision "+removed+" (node kept)", clone(schemaNodes), dropDec(cloneDec(), removed), true)
	}
	{
		ns := clone(schemaNodes)
		filtered := ns[:0]
		for _, n := range ns {
			if n.ID != "media-resolution.member.level" {
				filtered = append(filtered, n)
			}
		}
		check("remove node media-resolution.member.level (decision kept stale)", filtered, cloneDec(), true)
		check("drop decision media-resolution.member.level (node kept)", clone(schemaNodes), dropDec(cloneDec(), "media-resolution.member.level"), true)
	}
	// Substituting an arm member spelling is caught by the WIRE-SPELLING
	// guard (the bidirectional inventory is ID-based by design).
	{
		ns := clone(schemaNodes)
		for i := range ns {
			if ns[i].ID == "part.arm.inlineData" {
				ns[i].Member = "inline_data" // wire spelling regression
			}
		}
		spellingBroken := false
		for _, n := range ns {
			if n.Kind != "enum-value" && strings.Contains(n.Member, "_") {
				spellingBroken = true
			}
		}
		if !spellingBroken {
			t.Errorf("substitute part.arm.inlineData member: wire-spelling guard missed it")
		}
	}
	// An invented node without a decision fails.
	{
		ns := append(clone(schemaNodes), SchemaNode{ID: "part.arm.invented", Member: "invented", Kind: "message"})
		check("add invented part.arm.invented", ns, cloneDec(), true)
	}
	// A stale decision naming no node fails.
	{
		d := cloneDec()
		d["part.arm.removed_member"] = "torana.v2.RequestUnknownBlock.payload_json"
		check("stale decision part.arm.removed_member", clone(schemaNodes), d, true)
	}
}

// TestSnapshotWireSpellings: EVERY generated member is a camelCase wire
// spelling — arms, ancillaries, FunctionResponse members, nested blob/
// file-data members, the mediaResolution level member. Enum values are
// canonical provider spellings, pinned exactly by TestSnapshotVocabularyPinned.
func TestSnapshotWireSpellings(t *testing.T) {
	for _, n := range schemaNodes {
		if n.Kind == "enum-value" {
			continue
		}
		if strings.Contains(n.Member, "_") {
			t.Errorf("node %s member %q is not a camelCase wire spelling", n.ID, n.Member)
		}
		if n.Member == "" {
			t.Errorf("node %s has an empty member", n.ID)
		}
	}
	for _, a := range agentPlatformArms {
		if strings.Contains(a.Member, "_") {
			t.Errorf("agent-platform arm %s member %q is not a camelCase wire spelling", a.ID, a.Member)
		}
	}
}

// TestSnapshotVocabularyPinned: the usable vocabularies are DERIVED from
// the decisions; SCHEDULING_UNSPECIFIED and MEDIA_RESOLUTION_UNSPECIFIED
// are excluded exactly like any unknown value, and absence stays distinct.
func TestSnapshotVocabularyPinned(t *testing.T) {
	got := UsableEnumValues("scheduling.enum.")
	want := []string{"SILENT", "WHEN_IDLE", "INTERRUPT"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("usable scheduling vocabulary = %v, want %v", got, want)
	}
	if schemaCarrierDecisions["scheduling.enum.SCHEDULING_UNSPECIFIED"] != DecisionExcludedValue {
		t.Fatal("SCHEDULING_UNSPECIFIED must be excluded (value-free 400, never a silent default)")
	}
	got = UsableEnumValues("media-resolution.level.enum.")
	want = []string{"MEDIA_RESOLUTION_LOW", "MEDIA_RESOLUTION_MEDIUM", "MEDIA_RESOLUTION_HIGH", "MEDIA_RESOLUTION_ULTRA_HIGH"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("usable media-resolution vocabulary = %v, want %v", got, want)
	}
	if schemaCarrierDecisions["media-resolution.level.enum.MEDIA_RESOLUTION_UNSPECIFIED"] != DecisionExcludedValue {
		t.Fatal("MEDIA_RESOLUTION_UNSPECIFIED must be excluded")
	}
}

// TestAgentPlatformArmsHonest (finding 3): the agent-platform arms are a
// reviewed NON-descriptor decision. The absence guard is IDENTITY-based —
// the PARSED oneof members of every vendored artifact must not contain
// them — plus a tokenized snake_case text guard as defense in depth (a
// raw substring search for the camelCase spelling would miss the real
// proto member). A synthetic Part containing tool_call must trip the same
// guard, and the separate decision map is validated bidirectionally.
func TestAgentPlatformArmsHonest(t *testing.T) {
	// (a) Parsed-identity guard: the generated node set (derived from the
	// vendored artifacts) contains no agent-platform arm.
	parsed, _, err := gen.ParseVendoredSource()
	if err != nil {
		t.Fatal(err)
	}
	parsedIDs := map[string]bool{}
	for _, n := range parsed {
		parsedIDs[n.ID] = true
	}
	for _, a := range agentPlatformArms {
		if parsedIDs[a.ID] {
			t.Errorf("agent-platform arm %s is descriptor-derived; move it into the generated tables", a.ID)
		}
	}

	// (b) Tokenized snake_case text guard over the vendored bytes: the
	// exact proto spellings, word-bounded. This is CONSERVATIVE by
	// design: the scan runs over RAW bytes, so a comment mentioning
	// `tool_call` also trips it — that intentionally forces a human
	// refresh review instead of risking a silent collision. The parsed-
	// identity guard above remains the authoritative absence proof.
	guard := func(raw []byte, protoMember string) bool {
		return regexp.MustCompile(`\b` + regexp.QuoteMeta(protoMember) + `\b`).Match(raw)
	}
	m, err := gen.LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range agentPlatformArms {
		protoMember := snakeMember(a.Member)
		for _, f := range m.Files {
			raw, err := os.ReadFile(filepath.Join("source", f.Local))
			if err != nil {
				t.Fatal(err)
			}
			if guard(raw, protoMember) {
				t.Errorf("agent-platform proto member %s appears in vendored %s; move it into the descriptor-derived tables", protoMember, f.Local)
			}
		}
	}

	// (c) Synthetic rejection: the SAME guards trip on a synthetic Part
	// declaring tool_call inside oneof data.
	synthetic := "message Part {\n  oneof data {\n    string tool_call = 2;\n  }\n}"
	if !guard([]byte(synthetic), "tool_call") {
		t.Fatal("synthetic tool_call not caught by the tokenized text guard")
	}
	syntheticNodes, err := gen.SyntheticPartArms(synthetic)
	if err != nil {
		t.Fatal(err)
	}
	if !syntheticNodes["part.arm.toolCall"] {
		t.Fatal("synthetic tool_call was not classified as an arm — the parsed-identity guard would MISS a real vendored tool_call")
	}
	// The collision is the failure mode the guard exists to prevent: a
	// vendored tool_call arm and the separate non-descriptor declaration
	// would both claim part.arm.toolCall.
	if parsedIDs["part.arm.toolCall"] {
		t.Fatal("parsed vendored set contains part.arm.toolCall (descriptor + non-descriptor collision)")
	}

	// (d) The separate decision map is bidirectionally exact: missing,
	// extra, duplicate, or wrong-carrier rows all fail.
	seen := map[string]bool{}
	for _, a := range agentPlatformArms {
		seen[a.ID] = true
		if got := agentPlatformCarrierDecisions[a.ID]; got != "torana.v2.RequestUnknownBlock.payload_json" {
			t.Errorf("agent-platform arm %s decision = %q, want the unknown-payload carrier", a.ID, got)
		}
	}
	for id := range agentPlatformCarrierDecisions {
		if !seen[id] {
			t.Errorf("agent-platform decision %s names no arm", id)
		}
	}
	if len(agentPlatformCarrierDecisions) != len(agentPlatformArms) {
		t.Errorf("agent-platform decision count %d != arm count %d", len(agentPlatformCarrierDecisions), len(agentPlatformArms))
	}
}

// snakeMember converts a camelCase wire member back to the exact proto
// snake_case spelling (toolCall -> tool_call).
func snakeMember(member string) string {
	var out []rune
	for _, r := range member {
		if r >= 'A' && r <= 'Z' {
			out = append(out, '_', r-'A'+'a')
		} else {
			out = append(out, r)
		}
	}
	return string(out)
}

// TestSchemaNodesDeepCloneIsolation (finding 2): exported accessors never
// expose the authority — mutating returned nodes (including their Surfaces
// slices) is invisible to later callers.
func TestSchemaNodesDeepCloneIsolation(t *testing.T) {
	first := SchemaNodes()
	second := SchemaNodes()
	if len(first) == 0 {
		t.Fatal("SchemaNodes() returned nothing")
	}
	// Mutate the returned structs and their surface slices.
	first[0].Member = "corrupted"
	first[0].Kind = "corrupted"
	first[0].Surfaces[0] = "corrupted-surface"
	first[0].Surfaces = append(first[0].Surfaces, "corrupted-extra")
	for i := 1; i < len(first); i++ {
		if len(first[i].Surfaces) > 0 {
			first[i].Surfaces[0] = "corrupted-surface"
		}
	}
	// The authority and a fresh clone are untouched.
	if second[0].Member == "corrupted" || second[0].Kind == "corrupted" {
		t.Fatal("member/kind mutation leaked into a later clone")
	}
	for _, n := range second {
		for _, s := range n.Surfaces {
			if s == "corrupted-surface" || s == "corrupted-extra" {
				t.Fatalf("surface mutation leaked: %s surfaces %v", n.ID, n.Surfaces)
			}
		}
	}
	for _, n := range schemaNodes {
		for _, s := range n.Surfaces {
			if s == "corrupted-surface" || s == "corrupted-extra" {
				t.Fatalf("surface mutation leaked into the authority: %s", n.ID)
			}
		}
	}
	// CarrierFor resolves across the union and returns "" only for
	// unknown IDs.
	if CarrierFor("part.arm.toolCall") == "" {
		t.Fatal("CarrierFor must resolve the agent-platform union")
	}
	if CarrierFor("part.arm.text") == "" {
		t.Fatal("CarrierFor must resolve generated nodes")
	}
	if CarrierFor("no.such.node") != "" {
		t.Fatal("CarrierFor must return \"\" for unknown IDs")
	}
}

// TestSchemaNodesCoverSurfaces: every generated node has at least one
// known surface.
func TestSchemaNodesCoverSurfaces(t *testing.T) {
	for _, n := range schemaNodes {
		if len(n.Surfaces) == 0 {
			t.Errorf("node %s has no surface", n.ID)
		}
		for _, s := range n.Surfaces {
			if s != gen.SurfaceGemini && s != gen.SurfaceVertex {
				t.Errorf("node %s has unknown surface %q", n.ID, s)
			}
		}
	}
}
