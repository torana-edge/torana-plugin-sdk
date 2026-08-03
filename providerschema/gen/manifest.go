package gen

// Manifest loading with FAIL-CLOSED structural validation: the manifest is
// an immutable-source declaration, not prose. Empty/malformed entries,
// internally inconsistent URL/SHA/path triples, duplicate locals or paths,
// or a missing required artifact all fail here — a vendor mistake can never
// silently produce a wrong snapshot.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// requiredArtifacts is the exact set of vendored files every snapshot is
// generated from.
var requiredArtifacts = []string{
	"generativelanguage-v1beta-content.proto",
	"aiplatform-v1-content.proto",
	"aiplatform-v1-tool.proto",
}

var (
	sha40Re = regexp.MustCompile(`^[0-9a-f]{40}$`)
	sha64Re = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// ManifestEntry is one vendored artifact's provenance.
type ManifestEntry struct {
	Local              string `json:"local"`
	UpstreamCommitSHA  string `json:"upstream_commit_sha"`
	UpstreamCommitDate string `json:"upstream_commit_date"`
	Path               string `json:"path"`
	URL                string `json:"url"`
	SHA256             string `json:"sha256"`
}

// Manifest is the parsed vendored manifest.
type Manifest struct {
	FetchedAt string          `json:"fetched_at"`
	License   string          `json:"license"`
	Upstream  string          `json:"upstream"`
	Files     []ManifestEntry `json:"files"`
}

// manifestFile is the vendored provenance manifest (relative to the
// providerschema directory; generate.sh and the tests run there).
var manifestFile = filepath.Join("source", "manifest.json")

// LoadManifest reads and structurally validates the vendored manifest.
func LoadManifest() (*Manifest, error) {
	raw, err := os.ReadFile(manifestFile)
	if err != nil {
		return nil, fmt.Errorf("read vendored manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse vendored manifest: %w", err)
	}
	if err := validateManifest(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

// validateManifest enforces the immutable-source declaration rules.
func validateManifest(m *Manifest) error {
	if m.FetchedAt == "" {
		return fmt.Errorf("manifest: fetched_at is empty")
	}
	if m.License == "" {
		return fmt.Errorf("manifest: license is empty")
	}
	if !strings.HasPrefix(m.Upstream, "https://github.com/") {
		return fmt.Errorf("manifest: upstream %q is not a github.com URL", m.Upstream)
	}
	repo := strings.TrimPrefix(m.Upstream, "https://github.com/")
	if repo == "" || strings.Contains(repo, "/") == false || strings.Count(repo, "/") != 1 {
		return fmt.Errorf("manifest: upstream %q is not owner/repo", m.Upstream)
	}
	if len(m.Files) != len(requiredArtifacts) {
		return fmt.Errorf("manifest: %d artifacts, want exactly %d", len(m.Files), len(requiredArtifacts))
	}
	locals := map[string]bool{}
	paths := map[string]bool{}
	for _, f := range m.Files {
		if f.Local == "" {
			return fmt.Errorf("manifest: entry with empty local name")
		}
		if locals[f.Local] {
			return fmt.Errorf("manifest: duplicate local %q", f.Local)
		}
		locals[f.Local] = true
		if f.Path == "" {
			return fmt.Errorf("manifest: %s has an empty upstream path", f.Local)
		}
		if paths[f.Path] {
			return fmt.Errorf("manifest: duplicate upstream path %q", f.Path)
		}
		paths[f.Path] = true
		if !sha40Re.MatchString(f.UpstreamCommitSHA) {
			return fmt.Errorf("manifest: %s commit SHA %q is not 40 lowercase hex", f.Local, f.UpstreamCommitSHA)
		}
		if !sha64Re.MatchString(f.SHA256) {
			return fmt.Errorf("manifest: %s digest %q is not 64 lowercase hex", f.Local, f.SHA256)
		}
		wantURL := "https://raw.githubusercontent.com/" + repo + "/" + f.UpstreamCommitSHA + "/" + f.Path
		if f.URL != wantURL {
			return fmt.Errorf("manifest: %s URL %q is inconsistent with repository %q + SHA %s + path %q (want %q)",
				f.Local, f.URL, repo, f.UpstreamCommitSHA, f.Path, wantURL)
		}
	}
	for _, req := range requiredArtifacts {
		if !locals[req] {
			return fmt.Errorf("manifest: required artifact %q missing", req)
		}
	}
	return nil
}

// VerifyVendoredDigests recomputes the sha256 of every vendored artifact
// and compares it with the manifest. A tampered or un-re-vendored artifact
// fails here.
func VerifyVendoredDigests(m *Manifest) error {
	for _, f := range m.Files {
		raw, err := os.ReadFile(filepath.Join("source", f.Local))
		if err != nil {
			return fmt.Errorf("read vendored %s: %w", f.Local, err)
		}
		sum := sha256.Sum256(raw)
		got := hex.EncodeToString(sum[:])
		if got != f.SHA256 {
			return fmt.Errorf("vendored %s digest %s != manifest %s (re-vendor and regenerate)", f.Local, got, f.SHA256)
		}
	}
	return nil
}
