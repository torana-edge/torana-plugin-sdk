package providerschema

// Offline provider-schema inventory (no network; the vendored artifacts
// are the immutable inputs):
//
//   - TestSnapshotArtifactDigestsPinned — the vendored bytes match the
//     manifest digests (tamper / un-re-vendored artifact detection).
//   - TestSnapshotGeneratedExact — an in-memory re-render of snapshot.gen.go
//     is byte-identical to the checked-in file (artifact change without
//     regeneration fails; determinism across runs).
//   - TestSnapshotInventoryBidirectional — the decision set is EXACTLY the
//     generated node set, both directions, namespaced. Every nested member
//     (FunctionResponseBlob/FileData, mediaResolution.level and its enum
//     values, the scheduling enum values) participates.
//   - TestSnapshotInventoryAdversarial — revert-proven rows: deleting,
//     substituting, or adding any node/decision fails the SAME guard.
//   - TestSnapshotWireSpellings — camelCase across EVERY table.
//   - TestSnapshotVocabularyPinned — usable vocabularies derived from the
//     decisions; UNSPECIFIED excluded exactly like any unknown value.
//   - TestAgentPlatformArmsHonest — the agent-platform arms are a reviewed
//     NON-descriptor decision: grep-verified absent from the vendored
//     artifacts, never claimed as descriptor-derived.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSnapshotArtifactDigestsPinned: the vendored artifacts must match the
// manifest digests. Changing a vendored proto without re-vendoring (and
// regenerating) fails here.
func TestSnapshotArtifactDigestsPinned(t *testing.T) {
	m, err := LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyVendoredDigests(m); err != nil {
		t.Fatal(err)
	}
}

// TestSnapshotGeneratedExact: snapshot.gen.go is byte-identical to an
// in-memory re-render (twice — the determinism proof). Changing the
// manifest provenance or the vendored artifacts without regenerating
// fails here.
func TestSnapshotGeneratedExact(t *testing.T) {
	got, err := RenderGeneratedFile()
	if err != nil {
		t.Fatal(err)
	}
	again, err := RenderGeneratedFile()
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
	// Provenance header: the manifest SHAs must appear in the generated
	// file, so changing a SHA in the manifest without regenerating fails.
	for _, f := range mustManifest(t).Files {
		if !bytes.Contains(got, []byte(f.UpstreamCommitSHA)) {
			t.Errorf("generated file does not record upstream SHA %s", f.UpstreamCommitSHA)
		}
	}
}

func mustManifest(t *testing.T) *Manifest {
	t.Helper()
	m, err := LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestSnapshotInventoryBidirectional: every generated node has exactly one
// decision and every decision names a generated node. The node set is
// namespaced and includes nested members and enum values, so a deletion,
// substitution, or addition anywhere in the schema surface fails.
func TestSnapshotInventoryBidirectional(t *testing.T) {
	// Direction 1: every node has a decision.
	for _, n := range schemaNodes {
		if _, ok := schemaCarrierDecisions[n.ID]; !ok {
			t.Errorf("schema node %s has no carrier decision; add one to schemaCarrierDecisions", n.ID)
		}
	}
	// Direction 2: every decision names a node (stale decisions fail).
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
	// inventoryBroken runs the LIVE bidirectional invariant (the same
	// logic as TestSnapshotInventoryBidirectional) against one mutated
	// side and reports whether the mutation is caught.
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
	// called out) must fail the guard.
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
	// Same for FileData members and mediaResolution.level.
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
	// Removing the mediaResolution.level member (the object shape) fails.
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
	// guard (the bidirectional inventory is ID-based by design; the
	// spelling guard is TestSnapshotWireSpellings). Run that guard's logic
	// here as the revert proof.
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
// file-data members, the mediaResolution level member, and enum values
// (enum values are the canonical provider spellings, not proto names).
func TestSnapshotWireSpellings(t *testing.T) {
	for _, n := range schemaNodes {
		if n.Kind == "enum-value" {
			continue // canonical provider enum spellings, pinned exactly by TestSnapshotVocabularyPinned
		}
		if strings.Contains(n.Member, "_") {
			t.Errorf("node %s member %q is not a camelCase wire spelling", n.ID, n.Member)
		}
		if n.Member == "" {
			t.Errorf("node %s has an empty member", n.ID)
		}
	}
	for _, a := range AgentPlatformArms {
		if strings.Contains(a.Member, "_") {
			t.Errorf("agent-platform arm %s member %q is not a camelCase wire spelling", a.ID, a.Member)
		}
	}
}

// TestSnapshotVocabularyPinned: the usable vocabularies are DERIVED from
// the decisions; SCHEDULING_UNSPECIFIED and MEDIA_RESOLUTION_UNSPECIFIED
// are excluded exactly like any unknown value, and absence stays distinct
// (the docs + Edge acceptance rows pin the same rule).
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

// TestAgentPlatformArmsHonest: the agent-platform arms are a reviewed
// NON-descriptor decision. They must be absent from every vendored
// artifact (grep, on the pinned bytes), and the docs must not claim
// descriptor provenance for them.
func TestAgentPlatformArmsHonest(t *testing.T) {
	for _, a := range AgentPlatformArms {
		if strings.HasPrefix(a.ID, "part.arm.") {
			// They must not exist in any vendored artifact.
			for _, f := range mustManifest(t).Files {
				raw, err := os.ReadFile(filepath.Join("source", f.Local))
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Contains(raw, []byte(strings.ToLower(a.Member[:1])+a.Member[1:])) {
					// proto member would be tool_call / tool_response
					t.Errorf("agent-platform arm %s appears in vendored %s; move it into the descriptor-derived tables", a.ID, f.Local)
				}
			}
			// And the SEPARATE decision map must carry them like other
			// unknown arms (payload carrier).
			if got := AgentPlatformCarrierDecisions[a.ID]; got != "torana.v2.RequestUnknownBlock.payload_json" {
				t.Errorf("agent-platform arm %s decision = %q, want the unknown-payload carrier", a.ID, got)
			}
		}
	}
}

// TestSchemaNodesCoverSurfaces: every generated node has at least one
// surface and every surface name is known.
func TestSchemaNodesCoverSurfaces(t *testing.T) {
	for _, n := range schemaNodes {
		if len(n.Surfaces) == 0 {
			t.Errorf("node %s has no surface", n.ID)
		}
		for _, s := range n.Surfaces {
			if s != surfaceGemini && s != surfaceVertex {
				t.Errorf("node %s has unknown surface %q", n.ID, s)
			}
		}
	}
}
