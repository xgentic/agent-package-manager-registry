package service_test

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xgentic/agent-package-manager-registry/internal/domain"
	"github.com/xgentic/agent-package-manager-registry/internal/domain/archive"
	"github.com/xgentic/agent-package-manager-registry/internal/fixtures"
	"github.com/xgentic/agent-package-manager-registry/internal/service"
)

var publishedAt = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

type harness struct {
	publish *service.PublishService
	query   *service.QueryService
	meta    *fakeMeta
	blobs   *fakeBlobs
	repo    service.Repository
	tempDir string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessWithConfig(t, func(*service.PublishConfig) {})
}

func newHarnessWithConfig(t *testing.T, tweak func(*service.PublishConfig)) *harness {
	t.Helper()

	meta := newFakeMeta()
	blobs := newFakeBlobs()
	tempDir := filepath.Join(t.TempDir(), "tmp")

	cfg := service.PublishConfig{
		TempDir:              tempDir,
		MaxArchiveBytes:      1 << 20,
		MaxUncompressedBytes: 4 << 20,
		MaxArchiveEntries:    100,
		AcceptedMediaTypes:   []string{archive.MediaTypeZip, archive.MediaTypeGzip},
		RequireVersionMatch:  true,
	}
	tweak(&cfg)

	repo, err := meta.CreateRepository(t.Context(), "corp-main", service.VisibilityPrivate, 0)
	if err != nil {
		t.Fatalf("CreateRepository() error = %v", err)
	}

	return &harness{
		publish: service.NewPublishService(meta, blobs, service.FixedClock{Time: publishedAt}, cfg),
		query:   service.NewQueryService(meta, blobs),
		meta:    meta,
		blobs:   blobs,
		repo:    repo,
		tempDir: tempDir,
	}
}

func (h *harness) request(t *testing.T, version string, mediaType string, body []byte) service.PublishRequest {
	t.Helper()

	id, err := domain.ParseIdentity(fixtures.DefaultIdentity)
	if err != nil {
		t.Fatalf("ParseIdentity() error = %v", err)
	}
	return service.PublishRequest{
		Repository: h.repo,
		Package:    id,
		Version:    domain.Version(version),
		MediaType:  mediaType,
		Body:       bytes.NewReader(body),
	}
}

// tempFiles reports what is left in the upload directory. TR-15 says the answer
// is always "nothing".
func (h *harness) tempFiles(t *testing.T) []string {
	t.Helper()

	entries, err := os.ReadDir(h.tempDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("reading temp directory: %v", err)
	}

	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestPublishStoresBlobAndMetadata(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	body := fixtures.ValidZip()
	sum := sha256.Sum256(body)

	got, err := h.publish.Publish(t.Context(), h.request(t, "1.0.0", archive.MediaTypeZip, body))
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	if want := domain.NewDigest(sum[:]); got.Digest != want {
		t.Errorf("Digest = %q, want %q — the digest must be of the exact uploaded bytes", got.Digest, want)
	}
	if got.SizeBytes != int64(len(body)) {
		t.Errorf("SizeBytes = %d, want %d", got.SizeBytes, len(body))
	}
	if got.MediaType != archive.MediaTypeZip {
		t.Errorf("MediaType = %q, want %q", got.MediaType, archive.MediaTypeZip)
	}
	if !got.PublishedAt.Equal(publishedAt) {
		t.Errorf("PublishedAt = %v, want the injected clock's %v", got.PublishedAt, publishedAt)
	}
	if h.blobs.count() != 1 {
		t.Errorf("blob count = %d, want 1", h.blobs.count())
	}
	if left := h.tempFiles(t); len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}

// req-rg-001: what /download serves must be byte-identical to what was
// uploaded, and must hash to the advertised digest.
func TestDownloadReturnsTheExactPublishedBytes(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	body := fixtures.ValidZip()

	published, err := h.publish.Publish(t.Context(), h.request(t, "1.0.0", archive.MediaTypeZip, body))
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	id, _ := domain.ParseIdentity(fixtures.DefaultIdentity)
	download, err := h.query.OpenDownload(t.Context(), h.repo, id, "1.0.0")
	if err != nil {
		t.Fatalf("OpenDownload() error = %v", err)
	}
	defer download.Body.Close()

	served, err := io.ReadAll(download.Body)
	if err != nil {
		t.Fatalf("reading download: %v", err)
	}
	if !bytes.Equal(served, body) {
		t.Error("served bytes differ from the published bytes")
	}

	sum := sha256.Sum256(served)
	if got := domain.NewDigest(sum[:]); got != published.Digest {
		t.Errorf("sha256(download) = %q, want the advertised %q", got, published.Digest)
	}
	// FR-07: no transcoding — the stored media type is replayed.
	if download.Version.MediaType != archive.MediaTypeZip {
		t.Errorf("MediaType = %q, want %q", download.Version.MediaType, archive.MediaTypeZip)
	}
}

// TR-07: a version row whose bytes are missing is an internal fault, not a 404.
// Reporting "no such version" would send the client looking for a bug in its
// own lockfile for a version that genuinely was published.
func TestMissingBlobIsAnInternalFaultNotANotFound(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	published, err := h.publish.Publish(t.Context(), h.request(t, "1.0.0", archive.MediaTypeZip, fixtures.ValidZip()))
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	// Simulate the failure publish ordering exists to prevent.
	if err := h.blobs.Delete(t.Context(), published.Digest); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	id, _ := domain.ParseIdentity(fixtures.DefaultIdentity)
	_, err = h.query.OpenDownload(t.Context(), h.repo, id, "1.0.0")
	if err == nil {
		t.Fatal("OpenDownload() error = nil after the blob vanished")
	}
	if errors.Is(err, domain.ErrNotFound) {
		t.Errorf("error = %v; it reads as ErrNotFound, so the handler would answer 404 "+
			"for a version that was published", err)
	}
	if !errors.Is(err, domain.ErrInternal) {
		t.Errorf("error = %v, want ErrInternal", err)
	}
}

// FR-13: a rejected publish leaves no blob, no metadata row and no temp file.
func TestRejectedPublishLeavesNothingBehind(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	body := fixtures.Hostile()["zip-slip.zip"]

	_, err := h.publish.Publish(t.Context(), h.request(t, "1.0.0", archive.MediaTypeZip, body))
	if !errors.Is(err, domain.ErrArchiveInvalid) {
		t.Fatalf("Publish() error = %v, want ErrArchiveInvalid", err)
	}

	if h.blobs.count() != 0 {
		t.Errorf("blob count = %d, want 0 — validation must complete before persistence", h.blobs.count())
	}
	id, _ := domain.ParseIdentity(fixtures.DefaultIdentity)
	if _, err := h.query.ListVersions(t.Context(), h.repo, id); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ListVersions() error = %v, want ErrNotFound", err)
	}
	if left := h.tempFiles(t); len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}

// FR-10: the 422 lists every failure, not the first one.
func TestValidationErrorCarriesEveryFailure(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	body := fixtures.Custom([]fixtures.Entry{
		{Name: "apm.yml", Body: fixtures.Manifest("wrong/name", "9.9.9")},
		{Name: "../../escape.txt", Body: "traversal\n"},
	})

	_, err := h.publish.Publish(t.Context(), h.request(t, "1.0.0", archive.MediaTypeZip, body))

	var invalid *domain.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("error type = %T, want *domain.ValidationError", err)
	}
	if len(invalid.Failures) < 3 {
		t.Errorf("failures = %v, want all three broken rules reported", invalid.Failures)
	}
}

func TestPublishRejectsUnsupportedMediaType(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	_, err := h.publish.Publish(t.Context(), h.request(t, "1.0.0", "application/x-7z-compressed", fixtures.ValidZip()))
	if !errors.Is(err, domain.ErrUnsupportedMediaType) {
		t.Errorf("Publish() error = %v, want ErrUnsupportedMediaType", err)
	}
	if left := h.tempFiles(t); len(left) != 0 {
		t.Errorf("temp files left behind: %v — the body must not even be read", left)
	}
}

// §3.3: a body that does not parse as its declared type is a 400, distinct from
// an archive that parses and breaks rules.
func TestPublishRejectsACorruptBody(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	_, err := h.publish.Publish(t.Context(), h.request(t, "1.0.0", archive.MediaTypeZip, fixtures.NotAnArchive()))
	if !errors.Is(err, domain.ErrBadRequest) {
		t.Errorf("Publish() error = %v, want ErrBadRequest", err)
	}
	if left := h.tempFiles(t); len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}

// TR-10: the cap trips during the stream, and the oversized body is not stored.
func TestPublishRejectsAnOversizedBody(t *testing.T) {
	t.Parallel()

	h := newHarnessWithConfig(t, func(cfg *service.PublishConfig) {
		cfg.MaxArchiveBytes = 512
	})

	_, err := h.publish.Publish(t.Context(), h.request(t, "1.0.0", archive.MediaTypeZip, fixtures.ValidZip()))
	if !errors.Is(err, domain.ErrPayloadTooLarge) {
		t.Fatalf("Publish() error = %v, want ErrPayloadTooLarge", err)
	}
	if h.blobs.count() != 0 {
		t.Errorf("blob count = %d, want 0", h.blobs.count())
	}
	if left := h.tempFiles(t); len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}

// TR-09: memory is flat regardless of archive size, so a body that is far
// larger than any sane buffer still fails on the cap rather than on memory.
func TestPublishStreamsRatherThanBuffering(t *testing.T) {
	t.Parallel()

	h := newHarnessWithConfig(t, func(cfg *service.PublishConfig) {
		cfg.MaxArchiveBytes = 1 << 16
	})

	req := h.request(t, "1.0.0", archive.MediaTypeZip, nil)
	req.Body = io.LimitReader(endlessReader{}, 1<<30) // 1 GiB of zeros

	if _, err := h.publish.Publish(t.Context(), req); !errors.Is(err, domain.ErrPayloadTooLarge) {
		t.Fatalf("Publish() error = %v, want ErrPayloadTooLarge", err)
	}
	if left := h.tempFiles(t); len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}

// FR-11: a repeat publish is a 409 whether or not the bytes match.
func TestRepublishConflicts(t *testing.T) {
	t.Parallel()

	// ADR-0008: identical bytes are still a conflict. A registry that accepted
	// a byte-identical republish would be making the tuple mutable in the one
	// case where nobody notices.
	differentButValid := fixtures.Custom([]fixtures.Entry{
		{Name: "apm.yml", Body: fixtures.Manifest(fixtures.DefaultIdentity, "1.0.0")},
		{Name: "README.md", Body: "# a different archive for the same version\n"},
	})

	for _, tt := range []struct {
		name string
		body []byte
	}{
		{"identical bytes", fixtures.ValidZip()},
		{"different bytes", differentButValid},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			if _, err := h.publish.Publish(t.Context(), h.request(t, "1.0.0", archive.MediaTypeZip, fixtures.ValidZip())); err != nil {
				t.Fatalf("first Publish() error = %v", err)
			}

			second := append([]byte(nil), tt.body...)
			_, err := h.publish.Publish(t.Context(), h.request(t, "1.0.0", archive.MediaTypeZip, second))
			if !errors.Is(err, domain.ErrVersionConflict) {
				t.Fatalf("second Publish() error = %v, want ErrVersionConflict", err)
			}

			var conflict *domain.ConflictError
			if !errors.As(err, &conflict) {
				t.Fatalf("error type = %T, want *domain.ConflictError", err)
			}
			if conflict.PreviousPublish.IsZero() || conflict.PreviousDigest == "" {
				t.Errorf("conflict = %+v, want the previous publish's digest and time", conflict)
			}
		})
	}
}

// TR-08: concurrency cannot produce two winners.
func TestConcurrentPublishesOfOneVersion(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	body := fixtures.ValidZip()

	const attempts = 8
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		succeeded int
	)

	wg.Add(attempts)
	for range attempts {
		go func() {
			defer wg.Done()

			_, err := h.publish.Publish(t.Context(), h.request(t, "1.0.0", archive.MediaTypeZip, body))
			if err == nil {
				mu.Lock()
				succeeded++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if succeeded != 1 {
		t.Errorf("%d publishes succeeded, want exactly 1", succeeded)
	}
}

// FR-35 / ADR-0011: Inspect is Publish without the commit, which is what makes
// the web pre-flight report predict the publish outcome.
func TestInspectValidatesWithoutCommitting(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	body := fixtures.ValidZip()

	inspection, err := h.publish.Inspect(t.Context(), h.request(t, "1.0.0", archive.MediaTypeZip, body))
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if !inspection.OK() {
		t.Errorf("Inspect() failures = %v, want none", inspection.Failures)
	}
	if inspection.Manifest.Name != fixtures.DefaultIdentity {
		t.Errorf("Manifest.Name = %q, want %q", inspection.Manifest.Name, fixtures.DefaultIdentity)
	}

	if h.blobs.count() != 0 {
		t.Errorf("blob count = %d, want 0 — Inspect must not commit", h.blobs.count())
	}
	if left := h.tempFiles(t); len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}

	// The same archive must then actually publish, or the report was a lie.
	if _, err := h.publish.Publish(t.Context(), h.request(t, "1.0.0", archive.MediaTypeZip, body)); err != nil {
		t.Errorf("Publish() after a clean Inspect() error = %v", err)
	}
}

func TestInspectReportsFailuresWithoutFailing(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	inspection, err := h.publish.Inspect(t.Context(),
		h.request(t, "1.0.0", archive.MediaTypeZip, fixtures.Hostile()["no-manifest.zip"]))
	if err != nil {
		t.Fatalf("Inspect() error = %v; a rule failure is a report, not an error", err)
	}
	if inspection.OK() {
		t.Fatal("Inspect() reported no failures for an archive with no apm.yml")
	}
	if inspection.Failures[0].Rule != domain.RuleManifestPresent {
		t.Errorf("first failure = %q, want %q", inspection.Failures[0].Rule, domain.RuleManifestPresent)
	}
}

func TestListVersionsIsCompleteAndNewestFirst(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	for _, v := range []string{"1.0.0", "1.1.0", "2.0.0"} {
		body := fixtures.ValidZipFor(fixtures.DefaultIdentity, v)
		if _, err := h.publish.Publish(t.Context(), h.request(t, v, archive.MediaTypeZip, body)); err != nil {
			t.Fatalf("Publish(%s) error = %v", v, err)
		}
	}

	id, _ := domain.ParseIdentity(fixtures.DefaultIdentity)
	versions, err := h.query.ListVersions(t.Context(), h.repo, id)
	if err != nil {
		t.Fatalf("ListVersions() error = %v", err)
	}

	// FR-19: the complete set. A truncated list silently resolves a client's
	// semver range to the wrong version.
	if len(versions) != 3 {
		t.Fatalf("ListVersions() = %d versions, want 3", len(versions))
	}
	if versions[0].Version != "2.0.0" {
		t.Errorf("first version = %q, want the newest publish", versions[0].Version)
	}
}

// TR-15: a process killed mid-upload must not leave a permanent temp file.
func TestSweepTempRemovesOrphanedUploads(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	orphan := filepath.Join(dir, "upload-123456.archive")
	if err := os.WriteFile(orphan, []byte("half an archive"), 0o600); err != nil {
		t.Fatalf("writing orphan: %v", err)
	}
	keep := filepath.Join(dir, "something-else.txt")
	if err := os.WriteFile(keep, []byte("not ours"), 0o600); err != nil {
		t.Fatalf("writing unrelated file: %v", err)
	}

	removed, err := service.SweepTemp(dir)
	if err != nil {
		t.Fatalf("SweepTemp() error = %v", err)
	}
	if removed != 1 {
		t.Errorf("SweepTemp() removed %d files, want 1", removed)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Error("orphaned upload survived the sweep")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Error("the sweep removed a file that was not an upload")
	}
}

func TestSweepTempOnAMissingDirectory(t *testing.T) {
	t.Parallel()

	removed, err := service.SweepTemp(filepath.Join(t.TempDir(), "never-created"))
	if err != nil {
		t.Errorf("SweepTemp() error = %v, want nil on a first boot", err)
	}
	if removed != 0 {
		t.Errorf("SweepTemp() removed %d, want 0", removed)
	}
}

func TestRepositoryServiceValidatesNames(t *testing.T) {
	t.Parallel()

	repos := service.NewRepositoryService(newFakeMeta())

	if _, err := repos.Create(t.Context(), "corp-main", service.VisibilityPrivate, 0); err != nil {
		t.Errorf("Create(corp-main) error = %v, want nil", err)
	}
	for _, name := range []string{"", "-leading-dash", "Upper", "has space", "has/slash", strings.Repeat("a", 100)} {
		if _, err := repos.Create(t.Context(), name, service.VisibilityPrivate, 0); !errors.Is(err, domain.ErrBadRequest) {
			t.Errorf("Create(%q) error = %v, want ErrBadRequest", name, err)
		}
	}

	// FR-29: an unknown repository is a 404, and a malformed name is
	// indistinguishable from an absent one.
	if _, err := repos.Get(t.Context(), "no-such-repo"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get(unknown) error = %v, want ErrNotFound", err)
	}
	if _, err := repos.Get(t.Context(), "Not A Name"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get(malformed) error = %v, want ErrNotFound", err)
	}
}

type endlessReader struct{}

func (endlessReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}
