//go:build windows

package live

import (
	"errors"
	"syscall"
)

// transientOpenError reports whether a failed open is worth retrying.
//
// Windows reports "somebody else has this file open without sharing writes" as
// ERROR_SHARING_VIOLATION, and some filter drivers (antivirus real-time scan,
// the search indexer, backup agents) report the same situation as
// ERROR_ACCESS_DENIED. Both clear on their own within milliseconds to seconds.
//
// The file is ours and is not read-only — a genuine permission problem shows up
// as a persistent failure and still ends up being reported, just after the
// retries in openLogFile are exhausted.
// errSharingViolation is ERROR_SHARING_VIOLATION. `syscall` does not export it
// (only golang.org/x/sys/windows does, and that is an indirect dependency here),
// so it is spelled out rather than pulling in a direct require for one constant.
const errSharingViolation = syscall.Errno(32)

func transientOpenError(err error) bool {
	return errors.Is(err, errSharingViolation) ||
		errors.Is(err, syscall.ERROR_ACCESS_DENIED)
}
