package conformance_test

import (
	"testing"

	"github.com/torana-edge/torana-plugin-sdk/pb"
	"google.golang.org/protobuf/proto"
)

func TestV1CanonicalMessagesRoundTrip(t *testing.T) {
	in := &pb.ChatRequest{Model: "test", Messages: []*pb.Message{{Role: "user", Content: "hello"}}}
	b, err := proto.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out pb.ChatRequest
	if err := proto.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(in, &out) {
		t.Fatalf("v1 protobuf round trip changed the request: %#v", &out)
	}
}

func TestV1StreamSuppressionIsRepresentable(t *testing.T) {
	b, err := proto.Marshal(&pb.StreamEventResult{Handled: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 {
		t.Fatal("handled stream suppression must not encode as pass-through")
	}
}

// TestV1TickResultIsDistinguishableFromNothing is the tick equivalent of the
// stream-suppression check above, and it exists for the same reason: an
// all-defaults protobuf message encodes to zero bytes, which the host reads as
// pass-through. Without the explicit flag, a plugin that did work but reported
// nothing would be indistinguishable from one that never ran.
func TestV1TickResultIsDistinguishableFromNothing(t *testing.T) {
	empty, err := proto.Marshal(&pb.TickResult{})
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("an all-defaults TickResult encoded to %d bytes; the zero-byte "+
			"assumption behind the handled flag no longer holds", len(empty))
	}

	handled, err := proto.Marshal(&pb.TickResult{Handled: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(handled) == 0 {
		t.Fatal("a handled tick result must not encode as pass-through")
	}
}

func TestV1TickRequestRoundTrips(t *testing.T) {
	in := &pb.TickRequest{TickId: 7, UnixMillis: 1769000000000, IntervalMs: 240000}
	b, err := proto.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out pb.TickRequest
	if err := proto.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(in, &out) {
		t.Fatalf("v1 protobuf round trip changed the tick: %#v", &out)
	}
}
