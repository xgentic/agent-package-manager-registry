package archive

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
)

// tarGzReader reads entries from a gzip-compressed tar stream.
type tarGzReader struct {
	src Source
}

func openTarGz(src Source) (reader, error) {
	// Validate the gzip header up front so a body that is not gzip at all
	// fails as a corrupt archive rather than as an empty one.
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	zr, err := gzip.NewReader(src)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	_ = zr.Close()

	return &tarGzReader{src: src}, nil
}

func (t *tarGzReader) walk(fn func(Entry, io.Reader) error) error {
	if _, err := t.src.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("%w: %w", ErrCorrupt, err)
	}

	zr, err := gzip.NewReader(t.src)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	defer func() { _ = zr.Close() }()

	tr := tar.NewReader(zr)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: %w", ErrCorrupt, err)
		}

		entry := Entry{
			Name:         header.Name,
			Kind:         tarKind(header),
			Mode:         fs.FileMode(header.Mode), //nolint:gosec // tar mode bits, reported not applied
			LinkTarget:   header.Linkname,
			DeclaredSize: header.Size,
		}

		if err := fn(entry, tr); err != nil {
			return err
		}
	}
}

func tarKind(h *tar.Header) Kind {
	switch h.Typeflag {
	case tar.TypeReg:
		return KindFile
	case tar.TypeDir:
		return KindDir
	// Typeflag is what makes link detection reliable in tar; a mode check would
	// not see these (TR-11).
	case tar.TypeSymlink:
		return KindSymlink
	case tar.TypeLink:
		return KindHardlink
	default:
		return KindOther
	}
}
