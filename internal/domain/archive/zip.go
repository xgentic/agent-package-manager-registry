package archive

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"strings"
)

// zipReader reads entries from a ZIP's central directory.
type zipReader struct {
	r *zip.Reader
}

func openZip(src Source, size int64) (reader, error) {
	// zip.NewReader(ra, size) reads the **central directory** — by
	// construction, not by convention. That is exactly TR-12: an archive whose
	// local file headers disagree with its central directory cannot show one
	// entry table to the validator and another to an extractor, because we only
	// ever consult the authoritative one.
	r, err := zip.NewReader(src, size)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	return &zipReader{r: r}, nil
}

func (z *zipReader) walk(fn func(Entry, io.Reader) error) error {
	for _, f := range z.r.File {
		entry := Entry{
			Name:         f.Name,
			Kind:         zipKind(f),
			Mode:         f.Mode(),
			DeclaredSize: int64(f.UncompressedSize64), //nolint:gosec // reporting only; never trusted for limits
		}

		// A symlink's target is its file body. Reading it is bounded to a path
		// length: the point is to report the target, not to follow it.
		if entry.Kind == KindSymlink {
			entry.LinkTarget = readLinkTarget(f)
		}

		body, err := openZipEntry(f, entry.Kind)
		if err != nil {
			return err
		}
		err = fn(entry, body)
		if closer, ok := body.(io.Closer); ok {
			_ = closer.Close()
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func openZipEntry(f *zip.File, kind Kind) (io.Reader, error) {
	if kind != KindFile {
		return strings.NewReader(""), nil
	}

	rc, err := f.Open()
	if err != nil {
		// A member that will not open is a corrupt archive, which the caller
		// reports as a parse failure against the offending entry.
		return nil, fmt.Errorf("%w: entry %q: %w", ErrCorrupt, f.Name, err)
	}
	return rc, nil
}

func zipKind(f *zip.File) Kind {
	mode := f.Mode()
	switch {
	// ZIP stores the file type in the external attributes; fs.ModeSymlink is
	// how archive/zip surfaces it. Missing this is the classic zip symlink
	// escape (TR-11).
	case mode&fs.ModeSymlink != 0:
		return KindSymlink
	case f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/"):
		return KindDir
	case mode&fs.ModeDevice != 0, mode&fs.ModeNamedPipe != 0, mode&fs.ModeSocket != 0, mode&fs.ModeCharDevice != 0:
		return KindOther
	default:
		return KindFile
	}
}

func readLinkTarget(f *zip.File) string {
	rc, err := f.Open()
	if err != nil {
		return ""
	}
	defer func() { _ = rc.Close() }()

	target, err := io.ReadAll(io.LimitReader(rc, 4096))
	if err != nil {
		return ""
	}
	return string(target)
}
