package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/xgentic/agent-package-manager-registry/internal/domain"
	"github.com/xgentic/agent-package-manager-registry/internal/domain/archive"
)

// tempFilePattern is the prefix the startup sweep looks for. Uploads in flight
// are the only files in the temp directory, so the prefix is a safety belt
// rather than a filter.
const tempFilePattern = "upload-*.archive"

// PublishConfig is the policy half of publishing: caps, accepted formats and
// the manifest-version rule.
type PublishConfig struct {
	TempDir              string
	MaxArchiveBytes      int64
	MaxUncompressedBytes int64
	MaxArchiveEntries    int
	AcceptedMediaTypes   []string
	RequireVersionMatch  bool
}

func (c PublishConfig) limits() archive.Limits {
	return archive.Limits{
		MaxEntries:           c.MaxArchiveEntries,
		MaxUncompressedBytes: c.MaxUncompressedBytes,
	}
}

// PublishService owns what "publish" means.
//
// It is called by the HTTP API, and will be called by the web UI's upload and
// by any future CLI import. Because the rules live here rather than in a
// handler, those front doors cannot drift on what a valid package is
// (FR-34, driver 4 of docs/specs/03-architecture.md §1).
type PublishService struct {
	meta  MetadataStore
	blobs BlobStore
	clock Clock
	cfg   PublishConfig
}

func NewPublishService(meta MetadataStore, blobs BlobStore, clock Clock, cfg PublishConfig) *PublishService {
	return &PublishService{meta: meta, blobs: blobs, clock: clock, cfg: cfg}
}

// PublishRequest is one upload.
type PublishRequest struct {
	Repository Repository
	Package    domain.Identity
	Version    domain.Version

	// MediaType is the request's Content-Type. Callers check it against policy
	// before reading a byte of Body; AcceptMediaType is re-applied here so a
	// non-HTTP caller cannot skip the check.
	MediaType string
	Body      io.Reader
}

// Inspection is the pre-flight report: what the archive is, and every rule it
// breaks. It is what the web UI renders before a producer confirms (FR-35), and
// it is produced by exactly the same pipeline Publish runs, which is what makes
// the report predictive rather than advisory.
type Inspection struct {
	Package   domain.Identity
	Version   domain.Version
	Digest    domain.Digest
	SizeBytes int64
	MediaType string
	Manifest  domain.Manifest
	Failures  []domain.RuleFailure
}

// OK reports whether a Publish of the same bytes would pass validation.
func (i Inspection) OK() bool { return len(i.Failures) == 0 }

// Inspect validates without committing: no blob, no metadata row, no trace.
func (s *PublishService) Inspect(ctx context.Context, req PublishRequest) (Inspection, error) {
	staged, err := s.stage(ctx, req)
	if err != nil {
		return Inspection{}, err
	}
	defer staged.cleanup()

	return staged.inspection, nil
}

// Publish validates and, only if everything passes, commits.
//
// Order is the subject of ADR-0011 and is not arbitrary:
//
//	stream+hash → inspect → validate → blob → metadata → cleanup
//
// Blob before metadata, because a blob with no row is invisible garbage that
// `gc` reclaims, while a row with no blob is a 404 on a version a client
// believes exists — and a broken lockfile (TR-07).
func (s *PublishService) Publish(ctx context.Context, req PublishRequest) (Version, error) {
	staged, err := s.stage(ctx, req)
	if err != nil {
		return Version{}, err
	}
	defer staged.cleanup()

	// FR-13: validation completes before persistence, and a rejected publish
	// leaves nothing behind.
	if !staged.inspection.OK() {
		return Version{}, domain.NewValidationError(staged.inspection.Failures)
	}

	if _, err := staged.file.Seek(0, io.SeekStart); err != nil {
		return Version{}, fmt.Errorf("rewinding upload: %w", err)
	}
	if err := s.blobs.Put(ctx, staged.inspection.Digest, staged.file); err != nil {
		return Version{}, fmt.Errorf("storing archive: %w", err)
	}

	manifestJSON, err := json.Marshal(staged.inspection.Manifest.Fields)
	if err != nil {
		// The manifest already parsed as YAML, so this cannot normally fail;
		// storing nothing is better than failing the publish over it.
		manifestJSON = nil
	}

	// The conflict is raised by the unique constraint inside CreateVersion,
	// never by a SELECT here (TR-08).
	return s.meta.CreateVersion(ctx, req.Repository.ID, NewVersion{
		Package:      req.Package,
		Version:      req.Version,
		Digest:       staged.inspection.Digest,
		SizeBytes:    staged.inspection.SizeBytes,
		MediaType:    staged.inspection.MediaType,
		PublishedAt:  s.clock.Now(),
		ManifestJSON: manifestJSON,
	})
}

// staged is an upload that has reached disk and been inspected.
type staged struct {
	file       *os.File
	inspection Inspection
	cleanup    func()
}

// stage runs steps 1–7 of ADR-0011: accept the media type, stream to a temp
// file while hashing, read the entry table, apply the eight rules.
func (s *PublishService) stage(_ context.Context, req PublishRequest) (staged, error) {
	mediaType, err := archive.AcceptMediaType(s.cfg.AcceptedMediaTypes, req.MediaType)
	if err != nil {
		return staged{}, fmt.Errorf("%w: %w", domain.ErrUnsupportedMediaType, err)
	}

	file, size, digest, err := s.ingest(req.Body)
	if err != nil {
		return staged{}, err
	}
	// TR-15: the temp file is removed on *every* path — success, validation
	// failure, corrupt archive, client disconnect.
	cleanup := func() {
		name := file.Name()
		_ = file.Close()
		_ = os.Remove(name)
	}

	inspection, err := archive.Inspect(mediaType, file, size, s.cfg.limits())
	if err != nil {
		cleanup()
		switch {
		case errors.Is(err, archive.ErrCorrupt):
			// §3.3: a body that does not parse as its declared type is a 400,
			// distinct from an archive that parses but breaks rules (422).
			return staged{}, fmt.Errorf("%w: %w", domain.ErrBadRequest, err)
		case errors.Is(err, archive.ErrUnsupportedMediaType):
			return staged{}, fmt.Errorf("%w: %w", domain.ErrUnsupportedMediaType, err)
		default:
			return staged{}, fmt.Errorf("inspecting archive: %w", err)
		}
	}

	result := domain.Validate(domain.ValidationInput{
		Package:             req.Package,
		Version:             req.Version,
		Inspection:          inspection,
		RequireVersionMatch: s.cfg.RequireVersionMatch,
	})

	return staged{
		file:    file,
		cleanup: cleanup,
		inspection: Inspection{
			Package:   req.Package,
			Version:   req.Version,
			Digest:    digest,
			SizeBytes: size,
			MediaType: mediaType,
			Manifest:  result.Manifest,
			Failures:  result.Failures,
		},
	}, nil
}

// ingest streams the body to a temp file while hashing it in the same pass.
//
// Memory is flat regardless of archive size: a 50 MB publish and a 1 MB publish
// cost the same in RAM (TR-09). The temp file exists because a ZIP's central
// directory is at the *end*, so validation needs random access — and buffering
// to get that would turn the size cap into a denial-of-service knob.
func (s *PublishService) ingest(body io.Reader) (*os.File, int64, domain.Digest, error) {
	if err := os.MkdirAll(s.cfg.TempDir, 0o750); err != nil {
		return nil, 0, "", fmt.Errorf("creating temp directory: %w", err)
	}

	file, err := os.CreateTemp(s.cfg.TempDir, tempFilePattern)
	if err != nil {
		return nil, 0, "", fmt.Errorf("creating temp file: %w", err)
	}
	discard := func() {
		name := file.Name()
		_ = file.Close()
		_ = os.Remove(name)
	}

	hasher := sha256.New()
	// Reading one byte past the cap is what distinguishes "exactly at the
	// limit" from "over it", and it stops mid-body rather than after a full
	// read (TR-10).
	limited := io.LimitReader(body, s.cfg.MaxArchiveBytes+1)

	size, err := io.Copy(file, io.TeeReader(limited, hasher))
	if err != nil {
		discard()
		return nil, 0, "", fmt.Errorf("reading upload: %w", err)
	}
	if s.cfg.MaxArchiveBytes > 0 && size > s.cfg.MaxArchiveBytes {
		discard()
		return nil, 0, "", fmt.Errorf("%w: archive exceeds the %d byte limit",
			domain.ErrPayloadTooLarge, s.cfg.MaxArchiveBytes)
	}

	if _, err := file.Seek(0, io.SeekStart); err != nil {
		discard()
		return nil, 0, "", fmt.Errorf("rewinding upload: %w", err)
	}
	return file, size, domain.NewDigest(hasher.Sum(nil)), nil
}

// SweepTemp removes upload temp files orphaned by a crash or a kill.
//
// It runs at startup, when nothing can be in flight (TR-15). Without it, every
// abrupt shutdown during a publish would leak an archive-sized file until
// someone noticed the disk.
func SweepTemp(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("reading temp directory: %w", err)
	}

	var removed int
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "upload-") {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
			return removed, fmt.Errorf("removing orphaned upload %s: %w", e.Name(), err)
		}
		removed++
	}
	return removed, nil
}
