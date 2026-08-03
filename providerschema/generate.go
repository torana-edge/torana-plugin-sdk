//go:build ignore

// generate.go — deterministic re-vendoring of the provider schema snapshot.
//
// Run via ./generate.sh (or `go run generate.go` from this directory):
// it parses the VENDORED artifacts (source/*.proto, pinned + digest-verified
// by source/manifest.json), renders snapshot.gen.go, and verifies the
// rendered file is byte-identical to a second in-memory render (the
// determinism proof). The generated file is then checked in together with
// the artifacts.
//
// This generator imports only the gen subpackage (stdlib-only), so it
// BOOTSTRAPS from vendored source + manifest even when snapshot.gen.go is
// absent or syntactically broken — regeneration never depends on its own
// old output (pinned by TestBootstrapWithoutGeneratedOutput).
//
// Update workflow (documented, deliberate):
//  1. Fetch the new upstream protos at their exact commit SHAs and
//     replace the vendored files + manifest entries (URL, SHA, digest,
//     fetch date).
//  2. Run ./generate.sh — the parser is strict: missing/unbalanced
//     messages and cross-surface conflicts are errors, not silent parses.
//  3. Review the snapshot.gen.go diff (the schema facts), then run the
//     offline suite. Ordinary tests and CI never need the network.
package main

import (
	"fmt"
	"os"

	"github.com/torana-edge/torana-plugin-sdk/providerschema/gen"
)

func main() {
	got, err := gen.RenderGeneratedFile()
	if err != nil {
		fmt.Fprintln(os.Stderr, "generate:", err)
		os.Exit(1)
	}
	again, err := gen.RenderGeneratedFile()
	if err != nil {
		fmt.Fprintln(os.Stderr, "generate:", err)
		os.Exit(1)
	}
	if string(got) != string(again) {
		fmt.Fprintln(os.Stderr, "generate: render is NOT deterministic (two runs differ)")
		os.Exit(1)
	}
	if err := os.WriteFile("snapshot.gen.go", got, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "generate:", err)
		os.Exit(1)
	}
	fmt.Printf("generate: wrote snapshot.gen.go (%d bytes, deterministic)\n", len(got))
}
