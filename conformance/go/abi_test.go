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
