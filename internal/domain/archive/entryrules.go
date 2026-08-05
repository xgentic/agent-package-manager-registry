package archive

import (
	"fmt"
	"io"
	"math"
	"path"
	"strings"
	"unicode"
)

// Limits are the caps enforced while reading (TR-13, TR-14). They are applied
// to what actually comes out of the decompressor, never to what the archive
// declares — declared sizes are attacker-controlled.
type Limits struct {
	MaxEntries           int
	MaxUncompressedBytes int64
}

// Unsafe is one entry that could not be extracted safely, with the reason
// stated in producer-facing terms.
type Unsafe struct {
	Name   string
	Reason string
}

// LimitKind names which cap an archive breached, if any.
type LimitKind string

const (
	LimitNone         LimitKind = ""
	LimitEntries      LimitKind = "entries"
	LimitUncompressed LimitKind = "uncompressed"
)

// Inspection is what an archive turned out to contain. It is a report of facts:
// which rules those facts break is decided in internal/domain/validation.go,
// which is what keeps the wire vocabulary out of the parser.
type Inspection struct {
	MediaType     string
	Entries       []Entry
	Unsafe        []Unsafe
	EntryCount    int
	ExpandedBytes int64
	LimitExceeded LimitKind

	// ManifestFound reports whether apm.yml exists at the archive root;
	// Manifest carries its bytes.
	ManifestFound bool
	Manifest      []byte

	// ParseFailures are entries that would not decompress. The archive opened,
	// so it is not a 400, but it is not sound either.
	ParseFailures []Unsafe
}

// Inspect reads an archive once and reports what it contains.
//
// One pass does everything: entry metadata, safety classification, real
// expansion measurement, and capture of apm.yml. Nothing is written to disk —
// extraction is the operation these archives attack, so validating by
// extracting would mean performing the attack to detect it (ADR-0011).
func Inspect(mediaType string, src Source, size int64, limits Limits) (Inspection, error) {
	var r reader
	var err error

	switch NormaliseMediaType(mediaType) {
	case MediaTypeZip:
		r, err = openZip(src, size)
	case MediaTypeGzip:
		r, err = openTarGz(src)
	default:
		return Inspection{}, fmt.Errorf("%w: %q", ErrUnsupportedMediaType, mediaType)
	}
	if err != nil {
		return Inspection{}, err
	}

	in := Inspection{MediaType: NormaliseMediaType(mediaType)}
	err = r.walk(func(entry Entry, body io.Reader) error {
		return in.consume(entry, body, limits)
	})

	// A cap breach stops the walk; that is the zip-bomb defence working, not a
	// failure to report.
	if err != nil && in.LimitExceeded != LimitNone {
		return in, nil
	}
	if err != nil {
		return Inspection{}, err
	}
	return in, nil
}

// errLimitReached unwinds the walk the moment a cap trips. It never escapes
// Inspect.
var errLimitReached = fmt.Errorf("archive limit reached")

func (in *Inspection) consume(entry Entry, body io.Reader, limits Limits) error {
	in.EntryCount++
	in.Entries = append(in.Entries, entry)

	if limits.MaxEntries > 0 && in.EntryCount > limits.MaxEntries {
		in.LimitExceeded = LimitEntries
		return errLimitReached
	}

	if reason := unsafeReason(entry); reason != "" {
		in.Unsafe = append(in.Unsafe, Unsafe{Name: entry.Name, Reason: reason})
		// An unsafe entry is not expanded: it has already failed, and reading
		// it would only spend work on an archive that is going to be rejected.
		return nil
	}

	if entry.Kind != KindFile {
		return nil
	}

	// Capture apm.yml on the way past, so the manifest costs no extra pass.
	if entry.Name == ManifestFileName {
		manifest, err := io.ReadAll(io.LimitReader(body, maxManifestBytes+1))
		if err != nil {
			in.ParseFailures = append(in.ParseFailures, Unsafe{Name: entry.Name, Reason: err.Error()})
			return nil
		}
		in.ManifestFound = true
		in.Manifest = manifest
		in.ExpandedBytes += int64(len(manifest))
		return in.checkExpansion(limits)
	}

	// Measure real expansion. io.Copy through a LimitReader means a bomb is
	// stopped by the reader rather than by trusting a declared size (TR-13).
	// The +1 is what makes "exactly at the cap" distinguishable from "over it".
	remaining := int64(math.MaxInt64)
	if limits.MaxUncompressedBytes > 0 {
		remaining = limits.MaxUncompressedBytes - in.ExpandedBytes + 1
	}
	n, err := io.Copy(io.Discard, io.LimitReader(body, remaining))
	in.ExpandedBytes += n
	if err != nil {
		in.ParseFailures = append(in.ParseFailures, Unsafe{Name: entry.Name, Reason: err.Error()})
		return nil
	}
	return in.checkExpansion(limits)
}

func (in *Inspection) checkExpansion(limits Limits) error {
	if limits.MaxUncompressedBytes > 0 && in.ExpandedBytes > limits.MaxUncompressedBytes {
		in.LimitExceeded = LimitUncompressed
		return errLimitReached
	}
	return nil
}

// unsafeReason classifies an entry that cannot be extracted safely, returning
// "" for a safe one. These are the rules of TR-11, and they run against the
// entry table rather than against extracted files.
func unsafeReason(e Entry) string {
	switch e.Kind {
	case KindSymlink:
		// A symlink can point anywhere; following it on extraction writes
		// outside the tree. The registry archive format has no use for one.
		return fmt.Sprintf("symlink to %q is not permitted in a registry archive", e.LinkTarget)
	case KindHardlink:
		return fmt.Sprintf("hardlink to %q is not permitted in a registry archive", e.LinkTarget)
	case KindOther:
		return "only regular files and directories are permitted"
	}

	name := e.Name
	switch {
	case name == "":
		return "entry has no name"
	case strings.ContainsRune(name, 0):
		return "entry name contains a NUL byte"
	case strings.HasPrefix(name, "/"):
		return "absolute paths are not permitted"
	// A backslash is a separator on Windows, so `..\..\evil` escapes there even
	// though path.Clean would leave it alone.
	case strings.ContainsRune(name, '\\'):
		return "backslashes are not permitted in entry names"
	// `C:evil` is drive-relative on Windows.
	case len(name) >= 2 && name[1] == ':':
		return "drive-qualified paths are not permitted"
	}

	for _, r := range name {
		if unicode.IsControl(r) {
			return "entry name contains control characters"
		}
	}

	// path.Clean resolves `a/../../b` to `../b`, which is the traversal this
	// check exists for. Comparing before cleaning would miss it.
	cleaned := path.Clean(name)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "entry escapes the archive root"
	}
	if path.IsAbs(cleaned) {
		return "absolute paths are not permitted"
	}
	return ""
}
