// Package archive reads registry archives without ever extracting them.
//
// This is the hostile-input surface: archive bytes are attacker-controlled, and
// the attacks (zip slip, symlinks, zip bombs, central-directory/local-header
// mismatch) are attacks *on extraction*. So nothing here extracts. It reads
// entry tables, measures expansion through bounded readers, and reports facts.
//
// The package deliberately imports nothing of ours — not even internal/domain.
// It reports what an archive *contains*; internal/domain/validation.go decides
// what that means on the wire. Keeping the two apart is what lets the parser be
// fuzzed and table-tested in isolation (TR-24).
package archive

import (
	"errors"
	"io"
	"io/fs"
)

// ManifestFileName is the file a registry archive must carry at its root.
const ManifestFileName = "apm.yml"

// maxManifestBytes bounds how much of apm.yml is read into memory. A manifest
// is a handful of scalars; anything larger is not one.
const maxManifestBytes = 1 << 20 // 1 MiB

// Sentinel errors. Callers map these to the wire: a body that cannot be opened
// as its declared type is a 400, and an unacceptable Content-Type is a 415
// (docs/specs/04-api-contract.md §3.3).
var (
	ErrCorrupt              = errors.New("archive does not parse as its declared type")
	ErrUnsupportedMediaType = errors.New("unsupported archive media type")
)

// Kind classifies an entry. Symlinks and hardlinks are separated from regular
// files because they are the entry types that let an archive reach outside its
// own tree, and detecting them is format-specific.
type Kind string

const (
	KindFile     Kind = "file"
	KindDir      Kind = "dir"
	KindSymlink  Kind = "symlink"
	KindHardlink Kind = "hardlink"
	KindOther    Kind = "other"
)

// Entry is one archive member.
//
// DeclaredSize is what the archive *claims*, and it is attacker-controlled — it
// is carried for reporting only. Real expansion is measured by reading
// (TR-13).
type Entry struct {
	Name         string
	Kind         Kind
	Mode         fs.FileMode
	LinkTarget   string
	DeclaredSize int64
}

// Source is what an archive is read from: the upload's temp file. Random access
// is required because a ZIP's central directory lives at the end of the file.
type Source interface {
	io.ReaderAt
	io.ReadSeeker
}

// reader walks an archive's entries in order, handing each body to fn. A body
// is valid only for the duration of that call.
type reader interface {
	walk(fn func(Entry, io.Reader) error) error
}
