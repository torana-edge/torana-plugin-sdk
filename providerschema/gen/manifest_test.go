package gen

// Manifest validation + bootstrap tests (finding 4): malformed manifest
// rows fail fail-closed, and the generator bootstraps from vendored
// source + manifest when the generated output is ABSENT.

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeManifest(t *testing.T, m *Manifest) string {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func validManifest() *Manifest {
	return &Manifest{
		FetchedAt: "2026-08-03",
		License:   "Apache-2.0 (googleapis)",
		Upstream:  "https://github.com/googleapis/googleapis",
		Files: []ManifestEntry{
			{
				Local:              "generativelanguage-v1beta-content.proto",
				UpstreamCommitSHA:  "bc7e3baa28fbb223fa93782e130260fab8205bfc",
				UpstreamCommitDate: "2025-12-18T08:25:06Z",
				Path:               "google/ai/generativelanguage/v1beta/content.proto",
				URL:                "https://raw.githubusercontent.com/googleapis/googleapis/bc7e3baa28fbb223fa93782e130260fab8205bfc/google/ai/generativelanguage/v1beta/content.proto",
				SHA256:             "8c01c50c6d6795bf9bc0d4036386fe031feaf67e560ae17e1fbc6c5b47b625e7",
			},
			{
				Local:              "aiplatform-v1-content.proto",
				UpstreamCommitSHA:  "fb6e47ad850029fd0c4deb96815550bd47bb42f2",
				UpstreamCommitDate: "2026-07-20T19:22:08Z",
				Path:               "google/cloud/aiplatform/v1/content.proto",
				URL:                "https://raw.githubusercontent.com/googleapis/googleapis/fb6e47ad850029fd0c4deb96815550bd47bb42f2/google/cloud/aiplatform/v1/content.proto",
				SHA256:             "3a3250f3ece784bfaf9bb8dbe7be44f476a68a1cfb890d670f3c079d2e634eb8",
			},
			{
				Local:              "aiplatform-v1-tool.proto",
				UpstreamCommitSHA:  "fb6e47ad850029fd0c4deb96815550bd47bb42f2",
				UpstreamCommitDate: "2026-07-20T19:22:08Z",
				Path:               "google/cloud/aiplatform/v1/tool.proto",
				URL:                "https://raw.githubusercontent.com/googleapis/googleapis/fb6e47ad850029fd0c4deb96815550bd47bb42f2/google/cloud/aiplatform/v1/tool.proto",
				SHA256:             "dac244e69b8d71a58cb86a4248fec3ef63dd7c3fc951669060c2256b79524053",
			},
		},
	}
}

func TestManifestValidationRows(t *testing.T) {
	good := validManifest()
	rows := []struct {
		name string
		mut  func(*Manifest)
	}{
		{"empty fetched_at", func(m *Manifest) { m.FetchedAt = "" }},
		{"empty license", func(m *Manifest) { m.License = "" }},
		{"empty upstream", func(m *Manifest) { m.Upstream = "" }},
		{"bad upstream form", func(m *Manifest) { m.Upstream = "https://github.com/onlyowner" }},
		{"wrong artifact count", func(m *Manifest) { m.Files = m.Files[:2] }},
		{"missing required artifact", func(m *Manifest) { m.Files = append(m.Files[:1], m.Files[2:]...) }},
		{"duplicate local", func(m *Manifest) { m.Files[1].Local = m.Files[0].Local }},
		{"duplicate path", func(m *Manifest) { m.Files[1].Path = m.Files[0].Path }},
		{"bad SHA length", func(m *Manifest) { m.Files[0].UpstreamCommitSHA = "abc" }},
		{"SHA uppercase", func(m *Manifest) { m.Files[0].UpstreamCommitSHA = strings.ToUpper(m.Files[0].UpstreamCommitSHA) }},
		{"bad digest", func(m *Manifest) { m.Files[0].SHA256 = "xyz" }},
		{"url/sha disagreement", func(m *Manifest) {
			m.Files[0].URL = strings.Replace(m.Files[0].URL, m.Files[0].UpstreamCommitSHA, "deadbeef", 1)
		}},
		{"url/path disagreement", func(m *Manifest) {
			m.Files[0].URL = strings.Replace(m.Files[0].URL, m.Files[0].Path, "other/path.proto", 1)
		}},
		{"empty local", func(m *Manifest) { m.Files[0].Local = "" }},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			m := validManifest()
			row.mut(m)
			p := writeManifest(t, m)
			if _, err := LoadManifestFrom(p); err == nil {
				t.Fatalf("malformed manifest accepted: %s", row.name)
			}
		})
	}
	// The valid manifest passes.
	p := writeManifest(t, good)
	if _, err := LoadManifestFrom(p); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
}

// TestBootstrapWithoutGeneratedOutput — a clean temporary copy of the
// vendored source + manifest + the gen package (NO snapshot.gen.go)
// compiles and runs the generator, and the produced output is
// byte-identical to the checked-in file. Regeneration never depends on
// its own old output.
func TestBootstrapWithoutGeneratedOutput(t *testing.T) {
	dir := t.TempDir()
	// Copy gen sources (the generator is stdlib-only) + source artifacts.
	copyDir := func(dst, src string) {
		entries, err := os.ReadDir(src)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			raw, err := os.ReadFile(filepath.Join(src, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dst, e.Name()), raw, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "gen"), 0o755); err != nil {
		t.Fatal(err)
	}
	copyDir(filepath.Join(dir, "gen"), ".")
	if err := os.MkdirAll(filepath.Join(dir, "source"), 0o755); err != nil {
		t.Fatal(err)
	}
	copyDir(filepath.Join(dir, "source"), filepath.Join("..", "source"))
	main := `package main

import (
	"fmt"
	"os"

	"tmpgen/gen"
)

func main() {
	got, err := gen.RenderGeneratedFile()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile("snapshot.gen.go", got, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	goMod := "module tmpgen\n\ngo 1.24\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("go", "run", "main.go")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("bootstrap generator failed (no snapshot.gen.go present): %v\n%s", err, out.String())
	}
	produced, err := os.ReadFile(filepath.Join(dir, "snapshot.gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	checkedIn, err := os.ReadFile(filepath.Join("..", "snapshot.gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(produced, checkedIn) {
		t.Fatal("bootstrapped output differs from the checked-in snapshot.gen.go")
	}
}

// TestManifestStrictDecodeRows (finding 2, round 3): provenance dates are
// structurally validated and the JSON decoding is fail-closed — duplicate
// keys, unknown members, and trailing JSON are all rejected.
func TestManifestStrictDecodeRows(t *testing.T) {
	valid := func() *Manifest { return validManifest() }
	rows := []struct {
		name string
		mut  func(*Manifest)
	}{
		{"fetched_at garbage", func(m *Manifest) { m.FetchedAt = "yesterday-ish" }},
		{"fetched_at wrong shape", func(m *Manifest) { m.FetchedAt = "03-08-2026" }},
		{"commit date garbage", func(m *Manifest) { m.Files[0].UpstreamCommitDate = "not a date" }},
		{"commit date wrong tz shape", func(m *Manifest) { m.Files[0].UpstreamCommitDate = "2025-12-18 08:25:06" }},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			m := valid()
			row.mut(m)
			p := writeManifest(t, m)
			if _, err := LoadManifestFrom(p); err == nil {
				t.Fatalf("malformed provenance accepted: %s", row.name)
			}
		})
	}

	// Raw JSON decode rows: duplicate keys, unknown members, trailing JSON.
	raw := func(m *Manifest) []byte {
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	// Duplicate "license" key.
	dup := string(raw(valid()))
	dup = strings.Replace(dup, `"license":"Apache-2.0 (googleapis)"`, `"license":"Apache-2.0 (googleapis)","license":"x"`, 1)
	writeRaw := func(t *testing.T, content string) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), "manifest.json")
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if _, err := LoadManifestFrom(writeRaw(t, dup)); err == nil {
		t.Fatal("duplicate manifest key accepted")
	}
	// Unknown member.
	unknown := strings.Replace(string(raw(valid())), `"fetched_at"`, `"fetched_on"`, 1)
	if _, err := LoadManifestFrom(writeRaw(t, unknown)); err == nil {
		t.Fatal("unknown manifest member accepted")
	}
	// Trailing JSON.
	trailing := string(raw(valid())) + `{"extra":true}`
	if _, err := LoadManifestFrom(writeRaw(t, trailing)); err == nil {
		t.Fatal("trailing JSON accepted")
	}
	// Nested duplicates: a duplicate field INSIDE one files[] entry (the
	// SHA/path/URL/date authority) must fail.
	nested := string(raw(valid()))
	nested = strings.Replace(nested,
		`"local":"generativelanguage-v1beta-content.proto"`,
		`"local":"generativelanguage-v1beta-content.proto","local":"x"`, 1)
	if _, err := LoadManifestFrom(writeRaw(t, nested)); err == nil {
		t.Fatal("duplicate key inside a files[] entry accepted")
	}
	// Duplicates inside an object nested THROUGH an array (array element
	// object keys) must fail.
	throughArray := `{"fetched_at":"2026-08-03","license":"l","upstream":"https://github.com/googleapis/googleapis",
  "files":[{"local":"a","local":"b"}]}`
	if _, err := LoadManifestFrom(writeRaw(t, throughArray)); err == nil {
		t.Fatal("duplicate key in an array-nested object accepted")
	}
	// Escape-equivalent keys collide: "li\u0063ense" decodes to "license",
	// which is already present.
	escaped := strings.Replace(string(raw(valid())), `"license":"Apache-2.0 (googleapis)"`, `"license":"Apache-2.0 (googleapis)","li\u0063ense":"x"`, 1)
	if _, err := LoadManifestFrom(writeRaw(t, escaped)); err == nil {
		t.Fatal("escape-equivalent duplicate key accepted")
	}
	// The valid manifest still passes.
	p := writeManifest(t, valid())
	if _, err := LoadManifestFrom(p); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
}
