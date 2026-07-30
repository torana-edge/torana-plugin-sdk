//go:build !wasip1

package sdktest_test

import (
	"context"
	"testing"

	sdk "github.com/torana-edge/torana-plugin-sdk"
	"github.com/torana-edge/torana-plugin-sdk/pb"
	"github.com/torana-edge/torana-plugin-sdk/sdktest"
)

// This file models a real plugin: it registers ONCE, in init(), exactly as a
// compiled plugin does. init() runs once per process, so nothing can put the
// handler back.
//
// sdktest.New must therefore not clear registrations on cleanup. An earlier
// version did, and it broke the primary use case while helping only the SDK's
// own tests: the first test passed, and every later one failed with "no handler
// registered" — a failure that looks like the plugin is broken.
//
// Two tests, deliberately. One would pass either way.

var seen int

func init() {
	sdk.OnBeforeRequest(func(ctx context.Context, req *pb.ChatRequest) (*pb.ChatRequest, error) {
		seen++
		req.Model = "handled"
		return req, nil
	})
}

func TestInitRegisteredHandlerSurvivesFirstTest(t *testing.T) {
	res := sdktest.New(t).BeforeRequest(&pb.ChatRequest{Model: "original"})
	if res.Request == nil || res.Request.Model != "handled" {
		t.Fatalf("the init-registered handler did not run: %+v", res.Request)
	}
}

func TestInitRegisteredHandlerSurvivesSecondTest(t *testing.T) {
	// This is the one that fails if New clears registrations: init() has
	// already run and will not run again.
	res := sdktest.New(t).BeforeRequest(&pb.ChatRequest{Model: "original"})
	if res.Request == nil || res.Request.Model != "handled" {
		t.Fatalf("the init-registered handler was lost between tests: %+v", res.Request)
	}
	if seen < 2 {
		t.Fatalf("handler ran %d times across two tests, want at least 2", seen)
	}
}
