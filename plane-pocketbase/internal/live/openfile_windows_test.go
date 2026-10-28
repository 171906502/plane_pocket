//go:build windows

package live

import (
	"errors"
	"os"
	"syscall"
	"testing"
)

// The retry in openLogFile is only as good as this predicate: retry on the two
// codes a momentary external holder produces, give up on everything else so a
// real permission problem is still reported instead of being swallowed by a
// one-second delay.
func TestTransientOpenError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"sharing violation", syscall.Errno(32), true},
		{"access denied", syscall.ERROR_ACCESS_DENIED, true},
		{"wrapped access denied", &os.PathError{Op: "open", Path: "x.log", Err: syscall.ERROR_ACCESS_DENIED}, true},
		{"file not found", os.ErrNotExist, false},
		{"disk full", syscall.ENOSPC, false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		if got := transientOpenError(c.err); got != c.want {
			t.Errorf("%s: transientOpenError(%v) = %v, want %v", c.name, c.err, got, c.want)
		}
	}
}

// A log that cannot be opened must still fail loudly once the retries are spent,
// rather than returning a half-built docLog.
func TestOpenDocLogReportsPersistentFailure(t *testing.T) {
	// A directory where the log file should be: opening it as a file cannot
	// succeed and is not a transient condition.
	dir := t.TempDir()
	document := "blocked"
	if err := os.MkdirAll(dir+string(os.PathSeparator)+safeFileName(document)+".log", 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, _, err := openDocLog(dir, document); err == nil {
		t.Fatal("openDocLog returned no error for an unopenable path")
	} else if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected error shape: %v", err)
	}
}
