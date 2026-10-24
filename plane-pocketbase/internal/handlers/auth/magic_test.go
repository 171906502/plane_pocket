package auth

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestDeliverMagicCodeWritesDedicatedFile pins the agreed Magic Link delivery
// contract: with PLANE_MAGIC_LOG set, the code lands in that file — and nowhere
// else — so it never mixes into the general application log.
func TestDeliverMagicCodeWritesDedicatedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magic.log")
	t.Setenv("PLANE_MAGIC_LOG", path)

	var buf bytes.Buffer
	restore := captureLog(&buf)
	defer restore()

	deliverMagicCode("alice@example.com", "135790")

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("magic log not written: %v", err)
	}
	line := string(raw)
	for _, want := range []string{"alice@example.com", "135790", "expires_at="} {
		if !strings.Contains(line, want) {
			t.Fatalf("magic log line missing %q: %q", want, line)
		}
	}
	if buf.Len() != 0 {
		t.Fatalf("code leaked into the process log: %q", buf.String())
	}
	// Mode bits are POSIX-only: Windows ignores the perm argument to OpenFile
	// and reports 0666 regardless, so the 0600 guarantee is asserted where it
	// is actually enforceable (deploy the log on a POSIX host, or restrict the
	// directory with NTFS ACLs on Windows).
	if runtime.GOOS != "windows" {
		if mode := fileMode(t, path); mode != 0o600 {
			t.Fatalf("magic log mode = %o, want 600 (a code is a bearer credential)", mode)
		}
	}
}

// TestDeliverMagicCodeAppends covers repeated sign-ins: the file must accumulate
// one line per request rather than truncating.
func TestDeliverMagicCodeAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magic.log")
	t.Setenv("PLANE_MAGIC_LOG", path)

	deliverMagicCode("alice@example.com", "111111")
	deliverMagicCode("bob@example.com", "222222")

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("magic log not written: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 appended lines, got %d: %q", len(lines), string(raw))
	}
	if !strings.Contains(lines[0], "111111") || !strings.Contains(lines[1], "222222") {
		t.Fatalf("append order wrong: %q", string(raw))
	}
}

// TestDeliverMagicCodeFallsBackToProcessLog pins the unset behaviour: without a
// configured file the code goes to the process log (Plane's dev behaviour), so
// a local run still works with no configuration.
func TestDeliverMagicCodeFallsBackToProcessLog(t *testing.T) {
	t.Setenv("PLANE_MAGIC_LOG", "")

	var buf bytes.Buffer
	restore := captureLog(&buf)
	defer restore()

	deliverMagicCode("carol@example.com", "246810")

	if !strings.Contains(buf.String(), "246810") {
		t.Fatalf("fallback log missing the code: %q", buf.String())
	}
}

// TestDeliverMagicCodeUnwritablePathStillLogs guards the failure mode: a bad
// path must not swallow the code, or the operator is locked out with no trace.
func TestDeliverMagicCodeUnwritablePathStillLogs(t *testing.T) {
	dir := t.TempDir()
	// A directory where a file is expected: OpenFile fails deterministically.
	t.Setenv("PLANE_MAGIC_LOG", filepath.Join(dir, "as-dir"))
	if err := os.Mkdir(filepath.Join(dir, "as-dir"), 0o700); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	restore := captureLog(&buf)
	defer restore()

	deliverMagicCode("dave@example.com", "975310")

	if !strings.Contains(buf.String(), "975310") {
		t.Fatalf("code lost when the log path is unusable: %q", buf.String())
	}
}

func captureLog(buf *bytes.Buffer) func() {
	prev := log.Writer()
	flags := log.Flags()
	log.SetOutput(buf)
	log.SetFlags(0)
	return func() {
		log.SetOutput(prev)
		log.SetFlags(flags)
	}
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}
