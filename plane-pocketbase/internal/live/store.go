package live

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// maxLogSize is a soft ceiling. Updates are appended, never merged in place
// (merging is a CRDT operation this package does not implement), so a document's
// log grows with its edit history: roughly 100–200 bytes per change. Compaction
// (hub.go) rewrites the log from a client snapshot once it passes the threshold,
// so reaching this size means compaction kept failing — 64 MiB is far beyond
// anything a hand-edited page reaches, but it is checked so that a runaway client
// cannot fill the disk silently.
const maxLogSize = 64 << 20

// docLog is the append-only update history of one document.
//
// File format: a flat sequence of records, each `[varuint length][bytes]`. No
// header, no index — reading is a linear scan, which is what a replay needs
// anyway. Appends never rewrite the file, so a crash can at worst leave a
// truncated final record, which the reader skips.
type docLog struct {
	mu     sync.Mutex
	file   *os.File
	path   string
	size   int64
	warned bool
}

// openDocLog opens (creating if needed) the log for a document and returns its
// existing records.
func openDocLog(dir, documentName string) (*docLog, [][]byte, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("create live dir: %w", err)
	}
	path := filepath.Join(dir, safeFileName(documentName)+".log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("open %s: %w", path, err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	records, err := readRecords(file)
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	// The filename is sanitised and hashed, so it cannot be reversed. The
	// sidecar is what lets CollectOrphans decide whether a log still belongs to
	// a page. Best-effort: a missing sidecar only means the log is never
	// collected automatically.
	writeNameSidecar(path, documentName)
	return &docLog{file: file, path: path, size: info.Size()}, records, nil
}

// nameSidecarPath is where a log remembers which document it belongs to.
func nameSidecarPath(logPath string) string { return logPath + ".name" }

func writeNameSidecar(logPath, documentName string) {
	sidecar := nameSidecarPath(logPath)
	if _, err := os.Stat(sidecar); err == nil {
		return
	}
	if err := os.WriteFile(sidecar, []byte(documentName), 0o644); err != nil {
		log.Printf("[live] could not write %s: %v", sidecar, err)
	}
}

// replace swaps the whole log for records, in one rename.
//
// This is the only way this package shrinks a log: it never merges updates (it
// cannot — that is a CRDT operation), so the replacement has to be a snapshot
// produced by a client that had applied every stored update. The previous
// content is kept as `<log>.bak` so a snapshot that turns out to be incomplete
// can still be recovered by hand.
func (l *docLog) replace(records [][]byte) (int64, error) {
	var buf bytes.Buffer
	for _, rec := range records {
		buf.Write(appendVarBytes(nil, rec))
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return 0, os.ErrClosed
	}
	if l.size > 0 {
		if err := copyFile(l.path, l.path+".bak"); err != nil {
			log.Printf("[live] backup %s failed: %v", l.path, err)
		}
	}
	tmp := l.path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return 0, fmt.Errorf("write %s: %w", tmp, err)
	}
	// Windows cannot replace a file that is still open, so the handle is
	// dropped for the duration of the rename.
	_ = l.file.Close()
	l.file = nil
	if err := os.Rename(tmp, l.path); err != nil {
		_ = os.Remove(tmp)
		l.reopen()
		return 0, fmt.Errorf("rename %s: %w", tmp, err)
	}
	if err := l.reopen(); err != nil {
		return 0, err
	}
	l.size = int64(buf.Len())
	l.warned = false
	return l.size, nil
}

// reopen (re)opens the underlying file for appending. Callers hold l.mu, except
// after a failed replace where the handle was already dropped.
func (l *docLog) reopen() error {
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("reopen %s: %w", l.path, err)
	}
	l.file = file
	return nil
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(to)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// readRecords parses the whole file. A truncated tail is dropped rather than
// treated as an error: that is the expected shape after a hard kill mid-write,
// and losing the last update is better than refusing to open the document.
func readRecords(r io.ReadSeeker) ([][]byte, error) {
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var records [][]byte
	rest := data
	for len(rest) > 0 {
		size, n, ok := readVarUint(rest)
		if !ok || uint64(len(rest)-n) < size {
			break // truncated tail
		}
		end := n + int(size)
		rec := make([]byte, size)
		copy(rec, rest[n:end])
		records = append(records, rec)
		rest = rest[end:]
	}
	return records, nil
}

// append writes one update record. An empty update is skipped: peers send those
// during the handshake and they carry nothing.
func (l *docLog) append(update []byte) error {
	if len(update) <= len(emptyUpdate) && isNoopUpdate(update) {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return os.ErrClosed
	}
	if l.size > maxLogSize {
		if !l.warned {
			l.warned = true
			log.Printf("[live] WARNING: %s is %d bytes (over the %d soft limit); still appending. "+
				"Compaction is either disabled (PLANE_LIVE_COMPACT_BYTES) or failing — see internal/live/hub.go.",
				l.path, l.size, int64(maxLogSize))
		}
	}
	record := appendVarBytes(nil, update)
	n, err := l.file.Write(record)
	l.size += int64(n)
	if err != nil {
		return fmt.Errorf("append %s: %w", l.path, err)
	}
	return nil
}

func (l *docLog) close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

// currentSize is the on-disk length, used to decide when a log is worth
// compacting.
func (l *docLog) currentSize() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.size
}

// isNoopUpdate reports whether an update is yjs's encoding of "no change".
func isNoopUpdate(update []byte) bool {
	return len(update) == 0 || (len(update) == 2 && update[0] == 0 && update[1] == 0)
}

// safeFileName maps a document name (a page id in practice, but the client
// controls it) onto a filesystem-safe name. The hash suffix keeps distinct
// names from colliding after sanitisation.
func safeFileName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
		if b.Len() >= 64 {
			break
		}
	}
	sum := sha1.Sum([]byte(name))
	if b.Len() == 0 {
		b.WriteString("doc")
	}
	return b.String() + "-" + hex.EncodeToString(sum[:4])
}
