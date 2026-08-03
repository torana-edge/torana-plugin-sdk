package gen

// Synthetic parser tests (finding 1): the classification and contract
// dimensions are proven on small synthetic protos — a new `data`-oneof arm
// is an ARM, a new direct Part field is an ANCILLARY, REQUIRED→OPTIONAL
// and repeated/optional changes are visible in the nodes, cross-surface
// conflicts fail, and malformed/unbalanced input fails. The pinned
// artifacts must additionally render byte-identically
// (TestSnapshotGeneratedExact in the parent package).

import (
	"strings"
	"testing"
)

const syntheticPartGL = `message Part {
  oneof data {
    string text = 2;
    Blob inline_data = 3;
    FunctionCall function_call = 4;
    FutureArm future_arm = 11;
  }
  oneof metadata {
    VideoMetadata video_metadata = 14 [(google.api.field_behavior) = OPTIONAL];
  }
  bool thought = 11;
  bytes thought_signature = 13 [(google.api.field_behavior) = OPTIONAL];
  google.protobuf.Struct part_metadata = 8;
}`

const syntheticPartVertex = `message Part {
  message MediaResolution {
    enum Level {
      MEDIA_RESOLUTION_UNSPECIFIED = 0;
      MEDIA_RESOLUTION_LOW = 1;
    }
    oneof value {
      Level level = 1;
    }
  }
  oneof data {
    string text = 1 [(google.api.field_behavior) = OPTIONAL];
    Blob inline_data = 2 [(google.api.field_behavior) = OPTIONAL];
    FunctionCall function_call = 5 [(google.api.field_behavior) = OPTIONAL];
    FutureArm future_arm = 12 [(google.api.field_behavior) = OPTIONAL];
  }
  MediaResolution media_resolution = 12;
}`

// partBody extracts the message body from a full synthetic proto (the
// real path always feeds messageFields bodies, never the message header).
func partBody(t *testing.T, full string) string {
	t.Helper()
	body, ok, err := extractMessage(full, "Part")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("synthetic Part missing")
	}
	return body
}

func TestSyntheticPartClassification(t *testing.T) {
	gl, err := partNodes(SurfaceGemini, partBody(t, syntheticPartGL))
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]SchemaNode{}
	for _, n := range gl {
		byID[n.ID] = n
	}
	// A new `data`-oneof arm is classified as an ARM by the oneof block,
	// never a hand-written name list.
	if n, ok := byID["part.arm.futureArm"]; !ok {
		t.Fatal("future_arm inside oneof data was not classified as an arm")
	} else if n.Oneof != "data" {
		t.Fatalf("futureArm oneof = %q, want data", n.Oneof)
	}
	if n, ok := byID["part.arm.text"]; !ok || n.Kind != "string" {
		t.Fatal("text arm missing or wrong kind")
	}
	// Direct non-oneof fields are ancillaries.
	if n, ok := byID["part.ancillary.thought"]; !ok {
		t.Fatal("thought (direct field) was not classified as an ancillary")
	} else if n.Oneof != "" {
		t.Fatalf("thought oneof = %q, want empty", n.Oneof)
	}
	if n, ok := byID["part.ancillary.partMetadata"]; !ok || n.Kind != "object" {
		t.Fatal("partMetadata ancillary missing or wrong kind")
	} else if n.Oneof != "" {
		t.Fatalf("partMetadata oneof = %q, want empty (direct field)", n.Oneof)
	}
	// The metadata oneof member is an ancillary with its oneof retained.
	if n, ok := byID["part.ancillary.videoMetadata"]; !ok {
		t.Fatal("videoMetadata missing")
	} else if n.Oneof != "metadata" {
		t.Fatalf("videoMetadata oneof = %q, want metadata", n.Oneof)
	} else if n.FieldBehavior != BehaviorOptional {
		t.Fatalf("videoMetadata behavior = %q, want OPTIONAL", n.FieldBehavior)
	}

	ap, err := partNodes(SurfaceVertex, partBody(t, syntheticPartVertex))
	if err != nil {
		t.Fatal(err)
	}
	apByID := map[string]SchemaNode{}
	for _, n := range ap {
		apByID[n.ID] = n
	}
	if n, ok := apByID["part.arm.futureArm"]; !ok {
		t.Fatal("vertex future_arm not an arm")
	} else if n.FieldBehavior != BehaviorOptional {
		t.Fatalf("vertex futureArm behavior = %q, want OPTIONAL", n.FieldBehavior)
	}
	if n, ok := apByID["part.ancillary.mediaResolution"]; !ok {
		t.Fatal("mediaResolution (direct field) not an ancillary")
	} else if n.Oneof != "" {
		t.Fatalf("mediaResolution oneof = %q, want empty (direct field)", n.Oneof)
	}
}

func TestSyntheticRequirednessAndCardinalityVisible(t *testing.T) {
	// REQUIRED->OPTIONAL must change the merged node.
	required := `message FunctionResponse {
  string name = 1 [(google.api.field_behavior) = REQUIRED];
  google.protobuf.Struct response = 2 [(google.api.field_behavior) = REQUIRED];
  string id = 3;
}`
	optional := `message FunctionResponse {
  string name = 1 [(google.api.field_behavior) = OPTIONAL];
  google.protobuf.Struct response = 2 [(google.api.field_behavior) = REQUIRED];
  repeated string id = 3;
}`
	reqBody, ok, err := extractMessage(required, "FunctionResponse")
	if err != nil || !ok {
		t.Fatalf("required synthetic: ok=%v err=%v", ok, err)
	}
	optBody, ok, err := extractMessage(optional, "FunctionResponse")
	if err != nil || !ok {
		t.Fatalf("optional synthetic: ok=%v err=%v", ok, err)
	}
	reqNodes, err := memberNodes("function-response", false, SurfaceGemini, reqBody)
	if err != nil {
		t.Fatal(err)
	}
	optNodes, err := memberNodes("function-response", false, SurfaceGemini, optBody)
	if err != nil {
		t.Fatal(err)
	}
	reqByName := map[string]SchemaNode{}
	for _, n := range reqNodes {
		reqByName[n.ID] = n
	}
	optByName := map[string]SchemaNode{}
	for _, n := range optNodes {
		optByName[n.ID] = n
	}
	if reqByName["function-response.member.name"].FieldBehavior != BehaviorRequired {
		t.Fatal("REQUIRED annotation not retained")
	}
	if optByName["function-response.member.name"].FieldBehavior != BehaviorOptional {
		t.Fatal("REQUIRED->OPTIONAL change not visible")
	}
	if reqByName["function-response.member.id"].Repeated {
		t.Fatal("id must not be repeated in the required variant")
	}
	if !optByName["function-response.member.id"].Repeated {
		t.Fatal("repeated change not visible")
	}
	// Proto3 optional keyword is distinct.
	presence := `message FunctionResponse {
  optional bool will_continue = 5;
}`
	presBody, ok, err := extractMessage(presence, "FunctionResponse")
	if err != nil || !ok {
		t.Fatalf("presence synthetic: ok=%v err=%v", ok, err)
	}
	pn, err := memberNodes("function-response", false, SurfaceGemini, presBody)
	if err != nil {
		t.Fatal(err)
	}
	if len(pn) != 1 || !pn[0].Optional {
		t.Fatalf("proto3 optional presence not retained: %+v", pn)
	}
}

func TestSyntheticMergeConflicts(t *testing.T) {
	base := `message Part {
  oneof data {
    Blob inline_data = 3;
  }
}`
	baseBody, ok, err := extractMessage(base, "Part")
	if err != nil || !ok {
		t.Fatalf("base synthetic: ok=%v err=%v", ok, err)
	}
	conflict := func(name string, mutate func(*SchemaNode)) {
		t.Helper()
		a, err := partNodes(SurfaceGemini, baseBody)
		if err != nil {
			t.Fatal(err)
		}
		b, err := partNodes(SurfaceVertex, baseBody)
		if err != nil {
			t.Fatal(err)
		}
		mutate(&b[0])
		if name == "requiredness" {
			a[0].FieldBehavior = BehaviorRequired
		}
		if _, err := mergeNodes(append(a, b...)); err == nil {
			t.Fatalf("%s: cross-surface conflict was not rejected", name)
		}
	}
	conflict("member", func(n *SchemaNode) { n.Member = "inline_data" })
	conflict("kind", func(n *SchemaNode) { n.Kind = "string" })
	conflict("repeated", func(n *SchemaNode) { n.Repeated = true })
	conflict("optional", func(n *SchemaNode) { n.Optional = true })
	conflict("oneof", func(n *SchemaNode) { n.Oneof = "" })
	conflict("requiredness", func(n *SchemaNode) {
		n.FieldBehavior = BehaviorOptional // the other surface is REQUIRED -> explicit contradiction
	})

	// UNSPECIFIED + OPTIONAL merges to OPTIONAL (no error).
	a, err := partNodes(SurfaceGemini, baseBody)
	if err != nil {
		t.Fatal(err)
	}
	b, err := partNodes(SurfaceVertex, baseBody)
	if err != nil {
		t.Fatal(err)
	}
	b[0].FieldBehavior = BehaviorOptional
	merged, err := mergeNodes(append(a, b...))
	if err != nil {
		t.Fatalf("UNSPECIFIED + OPTIONAL must merge, got %v", err)
	}
	if merged[0].FieldBehavior != BehaviorOptional {
		t.Fatalf("merged behavior = %q, want OPTIONAL", merged[0].FieldBehavior)
	}
	// REQUIRED + UNSPECIFIED merges to REQUIRED.
	a[0].FieldBehavior = BehaviorRequired
	b[0].FieldBehavior = BehaviorUnspecified
	merged, err = mergeNodes(append(a, b...))
	if err != nil {
		t.Fatalf("REQUIRED + UNSPECIFIED must merge, got %v", err)
	}
	if merged[0].FieldBehavior != BehaviorRequired {
		t.Fatalf("merged behavior = %q, want REQUIRED", merged[0].FieldBehavior)
	}
}

func TestSyntheticMalformedFails(t *testing.T) {
	// Unbalanced braces are a parse error, never a truncated result.
	if _, err := braceBody("oneof data { string text = 2; "); err == nil {
		t.Fatal("unbalanced braces accepted")
	}
	if _, err := messageFields("message Part { oneof data { string text = 2; }"); err == nil {
		t.Fatal("unbalanced message accepted")
	}
	// A missing required message is an error from the extraction path.
	if _, ok, err := extractMessage("message Other {}", "Part"); err != nil || ok {
		t.Fatalf("missing message: ok=%v err=%v", ok, err)
	}
	// Nested block imbalance surfaces (line-anchored: the nested block
	// must start at a line boundary like the real files).
	if _, _, err := nestedBlockBody("message Part {\n  message MediaResolution {\n    enum Level {", "MediaResolution"); err == nil {
		t.Fatal("unbalanced nested block accepted")
	}
}

func TestWireMemberConversion(t *testing.T) {
	for in, want := range map[string]string{
		"inline_data": "inlineData", "function_response": "functionResponse",
		"code_execution_result": "codeExecutionResult", "text": "text", "part_metadata": "partMetadata",
	} {
		if got := wireMember(in); got != want {
			t.Fatalf("wireMember(%q) = %q, want %q", in, got, want)
		}
	}
	if strings.Contains(wireMember("video_metadata"), "_") {
		t.Fatal("wireMember output must be camelCase")
	}
}

// TestSyntheticFailClosedRegressions (finding 1, round 3): constructs the
// strict parser does not understand must either produce the CORRECT fact
// or a STABLE ERROR — never an incomplete successful inventory.
func TestSyntheticFailClosedRegressions(t *testing.T) {
	// (a) A map field is SUPPORTED: it produces a map-kind node (forcing a
	// carrier decision), it does not disappear.
	mapBody := `message Part {
  map<string, string> labels = 2;
}`
	body, ok, err := extractMessage(mapBody, "Part")
	if err != nil || !ok {
		t.Fatalf("map synthetic: ok=%v err=%v", ok, err)
	}
	ns, err := partNodes(SurfaceGemini, body)
	if err != nil {
		t.Fatalf("map field must parse, got %v", err)
	}
	found := false
	for _, n := range ns {
		if n.ID == "part.ancillary.labels" {
			found = true
			if n.Kind != "map" {
				t.Fatalf("map field kind = %q, want map", n.Kind)
			}
		}
	}
	if !found {
		t.Fatal("map field vanished from the inventory")
	}

	// (b) An explicit json_name overrides the wire member — never silently
	// substituted by the default camelCase spelling.
	jname := `message FunctionResponse {
  string request_id = 1 [json_name = "requestID"];
}`
	jb, ok, err := extractMessage(jname, "FunctionResponse")
	if err != nil || !ok {
		t.Fatalf("json_name synthetic: ok=%v err=%v", ok, err)
	}
	jn, err := memberNodes("function-response", false, SurfaceGemini, jb)
	if err != nil {
		t.Fatal(err)
	}
	if len(jn) != 1 || jn[0].Member != "requestID" {
		t.Fatalf("json_name not honored: %+v", jn)
	}

	// (c) An unsupported field behavior is a STABLE ERROR, never a silent
	// collapse to UNSPECIFIED.
	badBehavior := `message Part {
  string x = 1 [(google.api.field_behavior) = OUTPUT_ONLY];
}`
	bb, ok, err := extractMessage(badBehavior, "Part")
	if err != nil || !ok {
		t.Fatalf("behavior synthetic: ok=%v err=%v", ok, err)
	}
	if _, err := partNodes(SurfaceGemini, bb); err == nil {
		t.Fatal("unsupported field_behavior accepted")
	}

	// (d) An unrecognized field form is a stable error, not an omission.
	unrecognized := `message Part {
  string x = 1; whatever_this_is y = 2;
}`
	ub, ok, err := extractMessage(unrecognized, "Part")
	if err != nil || !ok {
		t.Fatalf("unrecognized synthetic: ok=%v err=%v", ok, err)
	}
	if _, err := partNodes(SurfaceGemini, ub); err == nil {
		t.Fatal("unrecognized construct accepted")
	}

	// (e) A malformed field tail is a stable error.
	badTail := `message Part {
  string x = 1 ) ;
}`
	tb, ok, err := extractMessage(badTail, "Part")
	if err != nil || !ok {
		t.Fatalf("tail synthetic: ok=%v err=%v", ok, err)
	}
	if _, err := partNodes(SurfaceGemini, tb); err == nil {
		t.Fatal("malformed field tail accepted")
	}
}

// TestSyntheticEnumFailClosed (finding 1, round 3): enum accounting —
// negative values are SUPPORTED, unsupported forms are stable errors.
func TestSyntheticEnumFailClosed(t *testing.T) {
	// Negative values parse.
	neg := "\n  POS = 1;\n  NEG = -1;\n"
	vals, err := enumBodyValues(neg)
	if err != nil {
		t.Fatalf("negative enum value rejected: %v", err)
	}
	if len(vals) != 2 || vals[1] != "NEG" {
		t.Fatalf("negative enum values: %v", vals)
	}
	// Trailing options parse.
	opt := "\n  POS = 1 [deprecated = true];\n"
	vals, err = enumBodyValues(opt)
	if err != nil || len(vals) != 1 || vals[0] != "POS" {
		t.Fatalf("enum value with options: %v err=%v", vals, err)
	}
	// Comments are skipped.
	cmt := "\n  // a comment\n  POS = 1;\n"
	vals, err = enumBodyValues(cmt)
	if err != nil || len(vals) != 1 {
		t.Fatalf("commented enum: %v err=%v", vals, err)
	}
	// An unsupported form errors rather than disappearing.
	bad := "\n  POS 42;\n"
	if _, err := enumBodyValues(bad); err == nil {
		t.Fatal("unrecognized enum declaration accepted")
	}
	// A value without a semicolon errors.
	noSemi := "\n  POS = 1\n"
	if _, err := enumBodyValues(noSemi); err == nil {
		t.Fatal("semicolon-less enum declaration accepted")
	}
}

// TestSyntheticFullLineConsumptionPins (finding 2, round 4): the strict
// scanner must reject every escape from the line-accounting invariant.
func TestSyntheticFullLineConsumptionPins(t *testing.T) {
	partOf := func(t *testing.T, full string) string {
		t.Helper()
		body, ok, err := extractMessage(full, "Part")
		if err != nil || !ok {
			t.Fatalf("synthetic: ok=%v err=%v", ok, err)
		}
		return body
	}

	// (a) A close line with a trailing declaration is NOT a close: the
	// tail must not disappear. The line sits INSIDE the oneof so the
	// message extraction cannot truncate the body at it.
	tailEscape := `message Part {
  oneof data {
    string text = 1;
    } invented_field = 9;
  }
}`
	if _, err := partNodes(SurfaceGemini, partOf(t, tailEscape)); err == nil {
		t.Fatal("`} invented_field = 9;` accepted (close-line tail escaped)")
	}

	// (b) An EMPTY json_name is a stable error, never a fallback to the
	// derived name.
	emptyName := `message Part {
  string text = 1 [json_name = ""];
}`
	if _, err := partNodes(SurfaceGemini, partOf(t, emptyName)); err == nil {
		t.Fatal("empty json_name accepted")
	}

	// (c) An inline option tail with an UNCLOSED bracket is a stable
	// error, never accepted as `[ ... ;`.
	unclosed := `message Part {
  string text = 1 [json_name = "x";
}`
	if _, err := partNodes(SurfaceGemini, partOf(t, unclosed)); err == nil {
		t.Fatal("unclosed option bracket accepted")
	}

	// (d) A digit-bearing json_name with a single-digit field number must
	// parse to the DECLARED wire member (the old LastIndex search would
	// mis-slice on the digits inside the option).
	digitName := `message FunctionResponse {
  string request_id = 1 [json_name = "v1"];
}`
	body, ok, err := extractMessage(digitName, "FunctionResponse")
	if err != nil || !ok {
		t.Fatalf("digitName synthetic: ok=%v err=%v", ok, err)
	}
	ns, err := memberNodes("function-response", false, SurfaceGemini, body)
	if err != nil {
		t.Fatalf("digit-bearing json_name refused: %v", err)
	}
	if len(ns) != 1 || ns[0].Member != "v1" {
		t.Fatalf("json_name not honored: %+v", ns)
	}

	// (e) A duplicated json_name is a stable error.
	dupName := `message Part {
  string text = 1 [json_name = "a", json_name = "b"];
}`
	if _, err := partNodes(SurfaceGemini, partOf(t, dupName)); err == nil {
		t.Fatal("duplicate json_name accepted")
	}

	// (f) A malformed json_name (no quoted value) is a stable error.
	malformed := `message Part {
  string text = 1 [json_name = unquoted];
}`
	if _, err := partNodes(SurfaceGemini, partOf(t, malformed)); err == nil {
		t.Fatal("malformed json_name accepted")
	}
}

// TestSyntheticRound5TailPins (finding round 5): opening-line tails,
// mandatory semicolons, and comment normalization.
func TestSyntheticRound5TailPins(t *testing.T) {
	partOf := func(t *testing.T, full string) string {
		t.Helper()
		body, ok, err := extractMessage(full, "Part")
		if err != nil || !ok {
			t.Fatalf("synthetic: ok=%v err=%v", ok, err)
		}
		return body
	}

	// (a) A oneof OPENING line with a declaration tail must error — the
	// tail cannot disappear (`oneof data { invented_field = 9;`).
	openTail := `message Part {
  oneof data { invented_field = 9;
    string text = 1;
  }
}`
	if _, err := partNodes(SurfaceGemini, partOf(t, openTail)); err == nil {
		t.Fatal("oneof open line with a declaration tail accepted")
	}

	// (b) A field WITHOUT a terminator is an error — empty tail and
	// comment-only tail both fail.
	noSemi := `message Part {
  string x = 1
  string y = 2;
}`
	if _, err := partNodes(SurfaceGemini, partOf(t, noSemi)); err == nil {
		t.Fatal("semicolon-less field accepted")
	}
	commentOnly := `message Part {
  string x = 1 // comment
  string y = 2;
}`
	if _, err := partNodes(SurfaceGemini, partOf(t, commentOnly)); err == nil {
		t.Fatal("comment-only tail without ';' accepted")
	}

	// (c) A trailing comment AFTER the terminator is legal.
	withComment := `message Part {
  string x = 1; // comment
  string y = 2;
}`
	ns, err := partNodes(SurfaceGemini, partOf(t, withComment))
	if err != nil {
		t.Fatalf("semicolon+comment tail refused: %v", err)
	}
	if len(ns) != 2 {
		t.Fatalf("semicolon+comment fields: %d nodes, want 2", len(ns))
	}

	// (d) An inline option with a trailing comment is normalized ONCE:
	// parsed correctly AND the following line is not consumed.
	optComment := `message Part {
  string x = 1 [json_name = "xw"]; // note
  string y = 2;
}`
	ns, err = partNodes(SurfaceGemini, partOf(t, optComment))
	if err != nil {
		t.Fatalf("inline option + comment refused: %v", err)
	}
	if len(ns) != 2 {
		t.Fatalf("inline option + comment consumed the next line: %d nodes", len(ns))
	}
	// NON-VACUOUS json_name assertion: exactly ONE node must carry the
	// declared wire member AND its ID. If the parser silently derived the
	// default member instead, the count is zero and the test fails.
	honored := 0
	for _, n := range ns {
		if n.Member == "xw" && n.ID == "part.ancillary.xw" {
			honored++
		}
	}
	if honored != 1 {
		t.Fatalf("json_name not honored with comment: honored=%d nodes=%+v", honored, ns)
	}

	// (e) `//` INSIDE a quoted option string is not a comment delimiter
	// (supported); an unterminated string is a stable error.
	slashInString := `message Part {
  string x = 1 [json_name = "a//b"];
}`
	ns, err = partNodes(SurfaceGemini, partOf(t, slashInString))
	if err != nil {
		t.Fatalf("// inside a quoted option string refused: %v", err)
	}
	if len(ns) != 1 || ns[0].Member != "a//b" {
		t.Fatalf("// inside the json_name not honored: %+v", ns)
	}
	unterminated := `message Part {
  string x = 1 [json_name = "a//b];
}`
	if _, err := partNodes(SurfaceGemini, partOf(t, unterminated)); err == nil {
		t.Fatal("unterminated quoted string in an option accepted")
	}
}

// TestSyntheticRound6OptionTailPins (finding round 6): option tails must
// be EXACTLY one root bracket group — trailing garbage and a second
// bracket group are rejected; a `]` inside a quoted json_name and the
// trailing-comment form are positive controls.
func TestSyntheticRound6OptionTailPins(t *testing.T) {
	partOf := func(t *testing.T, full string) string {
		t.Helper()
		body, ok, err := extractMessage(full, "Part")
		if err != nil || !ok {
			t.Fatalf("synthetic: ok=%v err=%v", ok, err)
		}
		return body
	}
	reject := func(name, full string) {
		t.Helper()
		if _, err := partNodes(SurfaceGemini, partOf(t, full)); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}

	// Trailing garbage after the root close.
	reject("trailing garbage", `message Part {
  string x = 1 [json_name = "x"] invented;
}`)
	// A second bracket group after the root close.
	reject("second bracket group", `message Part {
  string x = 1 [json_name = "x"][deprecated = true];
}`)
	// A close before any open.
	reject("close before open", `message Part {
  string x = 1 ]foo];
}`)
	// The root never closes.
	reject("unclosed root", `message Part {
  string x = 1 [json_name = "x";
}`)

	// Positive control: a `]` inside a QUOTED json_name is legal.
	ns, err := partNodes(SurfaceGemini, partOf(t, `message Part {
  string x = 1 [json_name = "a]b"];
}`))
	if err != nil {
		t.Fatalf("] inside a quoted json_name refused: %v", err)
	}
	honored := 0
	for _, n := range ns {
		if n.Member == "a]b" && n.ID == "part.ancillary.a]b" {
			honored++
		}
	}
	if honored != 1 {
		t.Fatalf("] inside a quoted json_name not honored: %+v", ns)
	}

	// Positive control: the trailing-comment form still parses and the
	// following field is not consumed.
	ns, err = partNodes(SurfaceGemini, partOf(t, `message Part {
  string x = 1 [json_name = "xw"]; // note
  string y = 2;
}`))
	if err != nil {
		t.Fatalf("trailing-comment form refused: %v", err)
	}
	if len(ns) != 2 {
		t.Fatalf("trailing-comment form consumed the next line: %d nodes", len(ns))
	}
}
