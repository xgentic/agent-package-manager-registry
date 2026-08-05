package archive_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/xgentic/agent-package-manager-registry/internal/domain/archive"
	"github.com/xgentic/agent-package-manager-registry/internal/fixtures"
)

var testLimits = archive.Limits{MaxEntries: 100, MaxUncompressedBytes: 1 << 20}

func inspect(t *testing.T, mediaType string, body []byte, limits archive.Limits) archive.Inspection {
	t.Helper()

	in, err := archive.Inspect(mediaType, bytes.NewReader(body), int64(len(body)), limits)
	if err != nil {
		t.Fatalf("Inspect() error = %v, want nil", err)
	}
	return in
}

func TestInspectValidArchives(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		mediaType string
		body      []byte
	}{
		{"zip", archive.MediaTypeZip, fixtures.ValidZip()},
		{"tar.gz", archive.MediaTypeGzip, fixtures.ValidTarGz()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			in := inspect(t, tt.mediaType, tt.body, testLimits)

			if len(in.Unsafe) != 0 {
				t.Errorf("Unsafe = %v, want none", in.Unsafe)
			}
			if in.LimitExceeded != archive.LimitNone {
				t.Errorf("LimitExceeded = %q, want none", in.LimitExceeded)
			}
			if !in.ManifestFound {
				t.Fatal("ManifestFound = false, want apm.yml found at the root")
			}
			if !bytes.Contains(in.Manifest, []byte("acme/web-skills")) {
				t.Errorf("Manifest = %q, want the declared name in it", in.Manifest)
			}
			if in.ExpandedBytes == 0 {
				t.Error("ExpandedBytes = 0, want the measured expansion")
			}
		})
	}
}

// TR-11: traversal, links and absolute paths are detected from the entry table,
// before anything is read or written.
func TestInspectRejectsUnsafeEntries(t *testing.T) {
	t.Parallel()

	hostile := fixtures.Hostile()

	tests := []struct {
		fixture   string
		mediaType string
		wantEntry string
	}{
		{"zip-slip.zip", archive.MediaTypeZip, "../../etc/passwd"},
		{"zip-slip-disguised.zip", archive.MediaTypeZip, "nested/../../../evil.sh"},
		{"zip-symlink.zip", archive.MediaTypeZip, "passwd-link"},
		{"backslash-traversal.zip", archive.MediaTypeZip, `..\..\evil.txt`},
		{"tar-symlink.tar.gz", archive.MediaTypeGzip, "passwd-link"},
		{"tar-hardlink.tar.gz", archive.MediaTypeGzip, "passwd-link"},
		{"tar-absolute-path.tar.gz", archive.MediaTypeGzip, "/etc/cron.d/backdoor"},
	}

	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			t.Parallel()

			body, ok := hostile[tt.fixture]
			if !ok {
				t.Fatalf("fixture %q is missing", tt.fixture)
			}

			in := inspect(t, tt.mediaType, body, testLimits)
			if len(in.Unsafe) == 0 {
				t.Fatalf("Unsafe = none for %s, want the entry rejected", tt.fixture)
			}

			var found bool
			for _, u := range in.Unsafe {
				if u.Name == tt.wantEntry {
					found = true
					if u.Reason == "" {
						t.Error("Reason is empty; a producer needs to know what to fix")
					}
				}
			}
			if !found {
				t.Errorf("Unsafe = %v, want it to name %q", in.Unsafe, tt.wantEntry)
			}
		})
	}
}

// TR-13: the bomb is stopped by measuring the expansion, not by trusting the
// declared size.
func TestInspectStopsAtTheExpansionCap(t *testing.T) {
	t.Parallel()

	body := fixtures.Hostile()["zip-bomb.zip"]
	limits := archive.Limits{MaxEntries: 100, MaxUncompressedBytes: 1 << 20}

	in := inspect(t, archive.MediaTypeZip, body, limits)

	if in.LimitExceeded != archive.LimitUncompressed {
		t.Fatalf("LimitExceeded = %q, want %q", in.LimitExceeded, archive.LimitUncompressed)
	}
	// The walk aborts on breach rather than expanding the whole bomb first.
	if in.ExpandedBytes > limits.MaxUncompressedBytes+1 {
		t.Errorf("ExpandedBytes = %d, want the read to stop just past the %d cap",
			in.ExpandedBytes, limits.MaxUncompressedBytes)
	}
}

func TestInspectStopsAtTheEntryCap(t *testing.T) {
	t.Parallel()

	in := inspect(t, archive.MediaTypeZip, fixtures.ValidZip(), archive.Limits{
		MaxEntries:           2,
		MaxUncompressedBytes: 1 << 20,
	})

	if in.LimitExceeded != archive.LimitEntries {
		t.Errorf("LimitExceeded = %q, want %q", in.LimitExceeded, archive.LimitEntries)
	}
}

// TR-12: zip.NewReader reads the central directory, so the entry list cannot be
// the one a crafted local file header advertises.
func TestZipEntriesComeFromTheCentralDirectory(t *testing.T) {
	t.Parallel()

	in := inspect(t, archive.MediaTypeZip, fixtures.Hostile()["central-directory-mismatch.zip"], testLimits)

	var names []string
	for _, e := range in.Entries {
		names = append(names, e.Name)
	}

	for _, name := range names {
		if name == "../evil.txt" {
			t.Fatalf("entry list = %v; it contains the local-header name, so the "+
				"central directory is not what is being read (TR-12)", names)
		}
	}
	var sawInnocent bool
	for _, name := range names {
		if name == "innocent.txt" {
			sawInnocent = true
		}
	}
	if !sawInnocent {
		t.Errorf("entry list = %v, want the central directory's %q", names, "innocent.txt")
	}
}

// A body that is not a container at all cannot be opened: that is a 400, not a
// 422 (§3.3 error table).
func TestInspectRejectsUnopenableBodies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		mediaType string
		body      []byte
	}{
		{"not an archive, declared zip", archive.MediaTypeZip, fixtures.NotAnArchive()},
		{"not an archive, declared gzip", archive.MediaTypeGzip, fixtures.NotAnArchive()},
		{"zip bytes declared as gzip", archive.MediaTypeGzip, fixtures.ValidZip()},
		{"truncated gzip", archive.MediaTypeGzip, fixtures.TruncatedGzip()},
		{"empty body", archive.MediaTypeZip, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := archive.Inspect(tt.mediaType, bytes.NewReader(tt.body), int64(len(tt.body)), testLimits)
			if err == nil {
				t.Fatal("Inspect() error = nil, want a corrupt-archive rejection")
			}
			if !errors.Is(err, archive.ErrCorrupt) {
				t.Errorf("error = %v, want it to wrap ErrCorrupt", err)
			}
		})
	}
}

func TestInspectRejectsUnknownMediaType(t *testing.T) {
	t.Parallel()

	_, err := archive.Inspect("application/x-7z-compressed", bytes.NewReader(fixtures.ValidZip()),
		int64(len(fixtures.ValidZip())), testLimits)
	if !errors.Is(err, archive.ErrUnsupportedMediaType) {
		t.Errorf("error = %v, want ErrUnsupportedMediaType", err)
	}
}

func TestAcceptMediaType(t *testing.T) {
	t.Parallel()

	accepted := []string{archive.MediaTypeZip, archive.MediaTypeGzip}

	tests := []struct {
		header  string
		want    string
		wantErr bool
	}{
		{"application/zip", archive.MediaTypeZip, false},
		{"application/gzip", archive.MediaTypeGzip, false},
		{"APPLICATION/ZIP", archive.MediaTypeZip, false},
		{"application/zip; charset=binary", archive.MediaTypeZip, false},
		{"application/x-gzip", "", true},
		{"application/octet-stream", "", true},
		{"text/plain", "", true},
		{"", "", true},
	}

	for _, tt := range tests {
		got, err := archive.AcceptMediaType(accepted, tt.header)
		if tt.wantErr {
			if err == nil {
				t.Errorf("AcceptMediaType(%q) error = nil, want a 415", tt.header)
			} else if !errors.Is(err, archive.ErrUnsupportedMediaType) {
				t.Errorf("AcceptMediaType(%q) error = %v, want ErrUnsupportedMediaType", tt.header, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("AcceptMediaType(%q) error = %v, want nil", tt.header, err)
		}
		if got != tt.want {
			t.Errorf("AcceptMediaType(%q) = %q, want %q", tt.header, got, tt.want)
		}
	}
}

// ADR-0004: format policy is one config value, so narrowing to gzip alone is a
// deployment change rather than a code change.
func TestMediaTypePolicyIsNarrowable(t *testing.T) {
	t.Parallel()

	gzipOnly := []string{archive.MediaTypeGzip}

	if _, err := archive.AcceptMediaType(gzipOnly, archive.MediaTypeZip); !errors.Is(err, archive.ErrUnsupportedMediaType) {
		t.Errorf("zip accepted under a gzip-only policy: %v", err)
	}
	if _, err := archive.AcceptMediaType(gzipOnly, archive.MediaTypeGzip); err != nil {
		t.Errorf("gzip rejected under a gzip-only policy: %v", err)
	}
}
