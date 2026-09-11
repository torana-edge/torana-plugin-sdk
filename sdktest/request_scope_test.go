package sdktest

import (
	"fmt"
	"sync"
	"testing"
	"time"

	sdk "github.com/torana-edge/torana-plugin-sdk"
)

func TestRequestMetadataIsScopedToExplicitRequest(t *testing.T) {
	h := New(t)
	first := h.NewRequest()
	second := h.NewRequest()
	first.with(func() {
		if herr, err := sdk.MetaSet("key", "first"); err != nil || herr != nil {
			t.Fatalf("MetaSet: err=%v herr=%v", err, herr)
		}
	})
	first.with(func() {
		got, herr, err := sdk.MetaGet("key")
		if err != nil || herr != nil || got != "first" {
			t.Fatalf("same request lost metadata: got=%q err=%v herr=%v", got, err, herr)
		}
	})
	second.with(func() {
		_, herr, err := sdk.MetaGet("key")
		if err != nil || herr == nil || !sdk.IsNotFound(herr) {
			t.Fatalf("metadata leaked into a new request: err=%v herr=%v", err, herr)
		}
	})
}

func TestConcurrentRequestsRetainMetadataOwnership(t *testing.T) {
	h := New(t)
	first := h.NewRequest()
	second := h.NewRequest()
	first.meta["seed"] = "first"
	second.meta["seed"] = "second"

	firstEntered := make(chan struct{})
	secondAttempted := make(chan struct{})
	secondEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		first.with(func() {
			close(firstEntered)
			<-releaseFirst
			if herr, err := sdk.MetaSet("write", "first"); err != nil || herr != nil {
				errCh <- fmt.Errorf("first MetaSet: err=%v herr=%v", err, herr)
			}
		})
	}()
	<-firstEntered
	go func() {
		defer wg.Done()
		close(secondAttempted)
		second.with(func() {
			close(secondEntered)
			if herr, err := sdk.MetaSet("write", "second"); err != nil || herr != nil {
				errCh <- fmt.Errorf("second MetaSet: err=%v herr=%v", err, herr)
			}
		})
	}()
	<-secondAttempted

	enteredTooSoon := false
	select {
	case <-secondEntered:
		enteredTooSoon = true
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseFirst)
	wg.Wait()
	if enteredTooSoon {
		t.Fatal("second request entered while the first request owned the harness scope")
	}
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
	if got := first.meta; got["seed"] != "first" || got["write"] != "first" {
		t.Fatalf("first request metadata ownership crossed: %v", got)
	}
	if got := second.meta; got["seed"] != "second" || got["write"] != "second" {
		t.Fatalf("second request metadata ownership crossed: %v", got)
	}
}

func TestRequestScopeRestoresHarnessAfterPanic(t *testing.T) {
	h := New(t)
	h.meta["harness"] = "original"
	panicking := h.NewRequest()
	panicking.meta["seed"] = "panicking"

	func() {
		defer func() {
			if recovered := recover(); recovered != "boom" {
				t.Fatalf("recovered = %v, want boom", recovered)
			}
		}()
		panicking.with(func() {
			if herr, err := sdk.MetaSet("write", "before-panic"); err != nil || herr != nil {
				t.Fatalf("MetaSet: err=%v herr=%v", err, herr)
			}
			panic("boom")
		})
	}()

	if got := panicking.meta; got["seed"] != "panicking" || got["write"] != "before-panic" {
		t.Fatalf("panicking request metadata was not captured: %v", got)
	}
	if got := h.meta; len(got) != 1 || got["harness"] != "original" {
		t.Fatalf("harness metadata was not restored after panic: %v", got)
	}

	next := h.NewRequest()
	next.meta["seed"] = "next"
	next.with(func() {
		got, herr, err := sdk.MetaGet("seed")
		if err != nil || herr != nil || got != "next" {
			t.Fatalf("next request after panic: got=%q err=%v herr=%v", got, err, herr)
		}
	})
}
