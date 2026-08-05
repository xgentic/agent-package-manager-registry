package fixtures_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/xgentic/agent-package-manager-registry/internal/fixtures"
)

// TestWriteFixtures materialises the archive set under testdata/archives.
//
// The bytes are generated (see the package doc), but they are also written to
// disk and committed, so an operator can replay a hostile archive against a
// running server by hand — `curl --data-binary @zip-slip.zip` — without
// building anything. The Go tool ignores testdata directories, so the hostile
// bytes are never compiled or vetted.
func TestWriteFixtures(t *testing.T) {
	dir := filepath.Join("testdata", "archives")

	if err := fixtures.Write(dir); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	// TR-25 names eight attacks; the set is larger, and the count is asserted
	// so a fixture cannot quietly disappear.
	if len(fixtures.Hostile()) < 8 {
		t.Errorf("Hostile() has %d archives, want at least the 8 of TR-25", len(fixtures.Hostile()))
	}
	if len(entries) != len(fixtures.Hostile())+3 {
		t.Errorf("%s holds %d files, want the hostile set plus valid.zip, valid.tar.gz and not-an-archive.zip",
			dir, len(entries))
	}
}

func TestOversizedIsActuallyLarge(t *testing.T) {
	t.Parallel()

	// The filler must survive compression, or the 413 test it feeds would
	// silently stop testing anything.
	if got := len(fixtures.Oversized(2 << 20)); got < 2<<20 {
		t.Errorf("Oversized(2 MiB) = %d bytes, want at least 2 MiB after compression", got)
	}
}
