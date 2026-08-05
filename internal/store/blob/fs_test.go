package blob_test

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/xgentic/agent-package-manager-registry/internal/domain"
	"github.com/xgentic/agent-package-manager-registry/internal/store/blob"
)

func newStore(t *testing.T) (*blob.FS, string) {
	t.Helper()

	root := filepath.Join(t.TempDir(), "blobs")
	store, err := blob.NewFS(root)
	if err != nil {
		t.Fatalf("NewFS() error = %v", err)
	}
	return store, root
}

func digestOf(payload []byte) domain.Digest {
	sum := sha256.Sum256(payload)
	return domain.NewDigest(sum[:])
}

func TestPutAndOpenRoundTrip(t *testing.T) {
	t.Parallel()

	store, root := newStore(t)
	payload := []byte("archive bytes")
	d := digestOf(payload)

	if err := store.Put(t.Context(), d, bytes.NewReader(payload)); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	// ADR-0003: the layout fans out on the digest so no directory holds every
	// archive in the registry.
	hex := d.Hex()
	want := filepath.Join(root, "sha256", hex[0:2], hex[2:4], hex)
	if _, err := os.Stat(want); err != nil {
		t.Errorf("blob not stored at %s: %v", want, err)
	}

	r, size, err := store.Open(t.Context(), d)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer r.Close()

	if size != int64(len(payload)) {
		t.Errorf("Open() size = %d, want %d", size, len(payload))
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading blob: %v", err)
	}
	// req-rg-001: the bytes served must be the bytes stored, exactly.
	if !bytes.Equal(got, payload) {
		t.Errorf("read %q, want %q", got, payload)
	}
}

// TR-04: the store never mutates an existing object, so a second Put of the
// same digest must leave the stored bytes alone.
func TestPutIsCreateIfAbsent(t *testing.T) {
	t.Parallel()

	store, _ := newStore(t)
	payload := []byte("original bytes")
	d := digestOf(payload)

	if err := store.Put(t.Context(), d, bytes.NewReader(payload)); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if err := store.Put(t.Context(), d, bytes.NewReader([]byte("something else entirely"))); err != nil {
		t.Fatalf("second Put() error = %v", err)
	}

	r, _, err := store.Open(t.Context(), d)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer r.Close()

	got, _ := io.ReadAll(r)
	if !bytes.Equal(got, payload) {
		t.Errorf("stored bytes = %q, want the original %q", got, payload)
	}
}

// A failed Put must not leave a partial object behind — a reader sees the whole
// object or none of it.
func TestFailedPutLeavesNothingBehind(t *testing.T) {
	t.Parallel()

	store, root := newStore(t)
	d := digestOf([]byte("never stored"))

	err := store.Put(t.Context(), d, io.MultiReader(
		bytes.NewReader([]byte("partial")),
		failingReader{},
	))
	if err == nil {
		t.Fatal("Put() error = nil, want the read failure surfaced")
	}

	if _, err := store.Stat(t.Context(), d); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Stat() error = %v, want ErrNotFound after a failed Put", err)
	}

	var leftovers []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, _ error) error {
		if info != nil && !info.IsDir() {
			leftovers = append(leftovers, path)
		}
		return nil
	})
	if len(leftovers) != 0 {
		t.Errorf("temp files left behind: %v", leftovers)
	}
}

func TestOpenAndStatUnknownDigest(t *testing.T) {
	t.Parallel()

	store, _ := newStore(t)
	d := digestOf([]byte("not stored"))

	if _, _, err := store.Open(t.Context(), d); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Open() error = %v, want ErrNotFound", err)
	}
	if _, err := store.Stat(t.Context(), d); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Stat() error = %v, want ErrNotFound", err)
	}
}

// A digest is used to build a filesystem path, so a malformed one must be
// rejected rather than joined into the path.
func TestMalformedDigestIsRejected(t *testing.T) {
	t.Parallel()

	store, _ := newStore(t)

	for _, d := range []domain.Digest{"", "sha256:../../etc/passwd", "sha256:zz", "../escape"} {
		if err := store.Put(t.Context(), d, bytes.NewReader(nil)); !errors.Is(err, domain.ErrBadRequest) {
			t.Errorf("Put(%q) error = %v, want ErrBadRequest", d, err)
		}
		if _, _, err := store.Open(t.Context(), d); !errors.Is(err, domain.ErrBadRequest) {
			t.Errorf("Open(%q) error = %v, want ErrBadRequest", d, err)
		}
	}
}

func TestDelete(t *testing.T) {
	t.Parallel()

	store, _ := newStore(t)
	payload := []byte("archive bytes")
	d := digestOf(payload)

	if err := store.Put(t.Context(), d, bytes.NewReader(payload)); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if err := store.Delete(t.Context(), d); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	// Deleting what is not there is not an error: gc must be re-runnable.
	if err := store.Delete(t.Context(), d); err != nil {
		t.Errorf("second Delete() error = %v, want nil", err)
	}
}

func TestPing(t *testing.T) {
	t.Parallel()

	store, root := newStore(t)
	if err := store.Ping(t.Context()); err != nil {
		t.Fatalf("Ping() error = %v, want nil", err)
	}

	// GET /ready must fail when the blob store is gone (FR-50).
	if err := os.RemoveAll(root); err != nil {
		t.Fatalf("removing blob root: %v", err)
	}
	if err := store.Ping(t.Context()); err == nil {
		t.Error("Ping() error = nil after the blob root vanished, want a failure")
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("disconnected mid-upload") }
