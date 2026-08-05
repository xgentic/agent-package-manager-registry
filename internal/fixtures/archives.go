// Package fixtures builds the archives the test suites publish — one valid
// archive per format, and the hostile set of TR-25.
//
// The malicious archives are **generated, not committed as opaque blobs**. A
// checked-in evil.zip is a file nobody can review: you cannot tell from reading
// the repository what makes it malicious, and a change to it is invisible in a
// diff. Building them here makes each attack legible as code and keeps the
// bytes reproducible.
//
// It is a normal package rather than a _test.go file because three suites need
// the same archives: the archive unit tests, the service tests and the
// conformance suite.
package fixtures

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// DefaultIdentity and DefaultVersion are what the valid archives declare, and
// what tests publish them as.
const (
	DefaultIdentity = "acme/web-skills"
	DefaultVersion  = "1.0.0"
)

// Manifest renders an apm.yml.
func Manifest(name, version string) string {
	return fmt.Sprintf("name: %s\nversion: %s\ndescription: A skill library\n", name, version)
}

// ValidZip is the archive shape `apm publish` produces: apm.yml and .apm/ at
// the root, not the `apm pack` plugin-bundle layout
// (docs/specs/04-api-contract.md §3.3).
func ValidZip() []byte { return ValidZipFor(DefaultIdentity, DefaultVersion) }

// ValidZipFor is ValidZip for a given identity and version.
func ValidZipFor(name, version string) []byte {
	return buildZip([]zipEntry{
		{name: "apm.yml", body: Manifest(name, version)},
		{name: ".apm/skills/review.md", body: "# review skill\n"},
		{name: "README.md", body: "# web skills\n"},
		{name: "LICENSE", body: "MIT\n"},
	})
}

// ValidTarGz is the same package in the other accepted format (FR-09).
func ValidTarGz() []byte { return ValidTarGzFor(DefaultIdentity, DefaultVersion) }

// ValidTarGzFor is ValidTarGz for a given identity and version.
func ValidTarGzFor(name, version string) []byte {
	return buildTarGz([]tarEntry{
		{name: "apm.yml", body: Manifest(name, version)},
		{name: ".apm/skills/review.md", body: "# review skill\n"},
	})
}

// Hostile is the archive set that must be rejected (TR-25). The map key is the
// filename used when the set is written to disk.
func Hostile() map[string][]byte {
	return map[string][]byte{
		// Entry escapes the extraction root.
		"zip-slip.zip": buildZip([]zipEntry{
			{name: "apm.yml", body: Manifest(DefaultIdentity, DefaultVersion)},
			{name: "../../etc/passwd", body: "root:x:0:0\n"},
		}),
		// The traversal only appears after path.Clean — which is why the check
		// cleans before comparing.
		"zip-slip-disguised.zip": buildZip([]zipEntry{
			{name: "apm.yml", body: Manifest(DefaultIdentity, DefaultVersion)},
			{name: "nested/../../../evil.sh", body: "#!/bin/sh\n"},
		}),
		// Extraction would follow the link and write outside the tree. In zip
		// the signal is a mode bit.
		"zip-symlink.zip": buildZip([]zipEntry{
			{name: "apm.yml", body: Manifest(DefaultIdentity, DefaultVersion)},
			{name: "passwd-link", body: "/etc/passwd", mode: fs.ModeSymlink | 0o777},
		}),
		// The same attack in tar, where the signal is Typeflag instead.
		"tar-symlink.tar.gz": buildTarGz([]tarEntry{
			{name: "apm.yml", body: Manifest(DefaultIdentity, DefaultVersion)},
			{name: "passwd-link", typeflag: tar.TypeSymlink, linkname: "/etc/passwd"},
		}),
		"tar-hardlink.tar.gz": buildTarGz([]tarEntry{
			{name: "apm.yml", body: Manifest(DefaultIdentity, DefaultVersion)},
			{name: "passwd-link", typeflag: tar.TypeLink, linkname: "/etc/passwd"},
		}),
		// Rooted at /, which extraction would honour.
		"tar-absolute-path.tar.gz": buildTarGz([]tarEntry{
			{name: "apm.yml", body: Manifest(DefaultIdentity, DefaultVersion)},
			{name: "/etc/cron.d/backdoor", body: "* * * * * root sh\n"},
		}),
		// Windows separator: `..\..\evil` escapes there while path.Clean leaves
		// it alone.
		"backslash-traversal.zip": buildZip([]zipEntry{
			{name: "apm.yml", body: Manifest(DefaultIdentity, DefaultVersion)},
			{name: `..\..\evil.txt`, body: "windows traversal\n"},
		}),
		// Tiny compressed, large expanded. Declared sizes are attacker
		// controlled, so the cap is measured on the way out (TR-13).
		"zip-bomb.zip": buildZip([]zipEntry{
			{name: "apm.yml", body: Manifest(DefaultIdentity, DefaultVersion)},
			{name: "bomb.txt", body: strings.Repeat("A", 8<<20)},
		}),
		"central-directory-mismatch.zip": centralDirectoryMismatch(),
		// The `apm pack` plugin-bundle layout: no apm.yml at the root.
		"no-manifest.zip": buildZip([]zipEntry{
			{name: "web-skills-1.0.0/plugin.json", body: `{"name":"web-skills"}`},
			{name: "web-skills-1.0.0/apm.yml", body: Manifest(DefaultIdentity, DefaultVersion)},
		}),
		"bad-manifest-yaml.zip": buildZip([]zipEntry{
			{name: "apm.yml", body: "name: [unterminated\n  ::::\n"},
		}),
		"manifest-missing-fields.zip": buildZip([]zipEntry{
			{name: "apm.yml", body: "description: no name, no version\n"},
		}),
		"manifest-wrong-name.zip": buildZip([]zipEntry{
			{name: "apm.yml", body: Manifest("someone-else/other", DefaultVersion)},
		}),
		"manifest-wrong-version.zip": buildZip([]zipEntry{
			{name: "apm.yml", body: Manifest(DefaultIdentity, "9.9.9")},
		}),
	}
}

// NotAnArchive is a body that is not a container at all. It is kept out of
// Hostile() because it is the 400 case, not a 422 (§3.3 error table).
func NotAnArchive() []byte { return []byte("this is not a zip file, it is a sentence") }

// TruncatedGzip has a valid gzip header and nothing usable after it.
func TruncatedGzip() []byte {
	full := ValidTarGz()
	return full[:len(full)/2]
}

// Oversized returns a valid archive whose *compressed* size exceeds payload
// bytes, for exercising the upload cap (413).
//
// The filler is pseudo-random rather than repeated text: deflate would squeeze
// a repeated string down to nothing, and the test would silently stop testing
// the cap. The generator is a fixed-seed LCG, so the bytes are reproducible.
func Oversized(payloadBytes int) []byte {
	filler := make([]byte, payloadBytes)
	state := uint64(0x2545F4914F6CDD1D)
	for i := range filler {
		state = state*6364136223846793005 + 1442695040888963407
		filler[i] = byte(state >> 33)
	}

	return buildZip([]zipEntry{
		{name: "apm.yml", body: Manifest(DefaultIdentity, DefaultVersion)},
		// Stored, not deflated: compressing random bytes only makes them
		// bigger, and the intent here is a body of a known size.
		{name: "filler.bin", body: string(filler), store: true},
	})
}

// Entry is one member of a custom archive.
type Entry struct {
	Name string
	Body string
	Mode fs.FileMode
}

// Custom builds a zip from an explicit entry list, for the one-off shapes a
// single test needs and no other suite shares.
func Custom(entries []Entry) []byte {
	converted := make([]zipEntry, len(entries))
	for i, e := range entries {
		converted[i] = zipEntry{name: e.Name, body: e.Body, mode: e.Mode}
	}
	return buildZip(converted)
}

// Write materialises every fixture under dir, so the set can also be replayed
// against a running server by hand.
func Write(dir string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("creating fixture directory: %w", err)
	}

	all := Hostile()
	all["valid.zip"] = ValidZip()
	all["valid.tar.gz"] = ValidTarGz()
	all["not-an-archive.zip"] = NotAnArchive()

	for name, body := range all {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o600); err != nil {
			return fmt.Errorf("writing fixture %s: %w", name, err)
		}
	}
	return nil
}

// --- builders --------------------------------------------------------------

type zipEntry struct {
	name  string
	body  string
	mode  fs.FileMode
	store bool // no compression, for content that must keep its size
}

func buildZip(entries []zipEntry) []byte {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	for _, e := range entries {
		method := zip.Deflate
		if e.store {
			method = zip.Store
		}

		header := &zip.FileHeader{Name: e.name, Method: method}
		if e.mode != 0 {
			header.SetMode(e.mode)
		}
		f, err := w.CreateHeader(header)
		must(err)
		_, err = io.WriteString(f, e.body)
		must(err)
	}
	must(w.Close())
	return buf.Bytes()
}

type tarEntry struct {
	name     string
	body     string
	typeflag byte
	linkname string
}

func buildTarGz(entries []tarEntry) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	for _, e := range entries {
		typeflag := e.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}

		header := &tar.Header{
			Name:     e.name,
			Typeflag: typeflag,
			Linkname: e.linkname,
			Mode:     0o644,
		}
		if typeflag == tar.TypeReg {
			header.Size = int64(len(e.body))
		}
		must(tw.WriteHeader(header))

		if typeflag == tar.TypeReg {
			_, err := io.WriteString(tw, e.body)
			must(err)
		}
	}
	must(tw.Close())
	must(gz.Close())
	return buf.Bytes()
}

// centralDirectoryMismatch writes a well-formed zip and then rewrites one
// *local* file header's name in place, leaving the central directory untouched.
//
// This is the archive TR-12 exists for: a reader that trusts local headers sees
// `../evil.txt`, while a reader of the central directory — which is what
// zip.NewReader uses — sees `innocent.txt`. The two tables disagree, and only
// one of them is authoritative.
func centralDirectoryMismatch() []byte {
	raw := buildZip([]zipEntry{
		{name: "apm.yml", body: Manifest(DefaultIdentity, DefaultVersion)},
		{name: "innocent.txt", body: "harmless\n"},
	})

	const (
		localHeaderSignature = 0x04034b50
		nameOffset           = 30
		nameLenOffset        = 26
	)

	patched := bytes.Clone(raw)
	for i := 0; i+nameOffset < len(patched); i++ {
		if binary.LittleEndian.Uint32(patched[i:]) != localHeaderSignature {
			continue
		}
		nameLen := int(binary.LittleEndian.Uint16(patched[i+nameLenOffset:]))
		if i+nameOffset+nameLen > len(patched) {
			continue
		}
		if string(patched[i+nameOffset:i+nameOffset+nameLen]) != "innocent.txt" {
			continue
		}
		copy(patched[i+nameOffset:i+nameOffset+nameLen], "../evil.txt\x00")
		break
	}
	return patched
}

// must panics on a builder error. These builders are deterministic and take no
// input, so a failure here is a bug in this file, not a test condition.
func must(err error) {
	if err != nil {
		panic("fixtures: " + err.Error())
	}
}
