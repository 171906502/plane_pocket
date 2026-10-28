//go:build !windows

package live

// transientOpenError reports whether a failed open is worth retrying.
//
// On Unix an O_CREATE|O_APPEND open of a file we own does not fail transiently:
// there is no mandatory locking, so another process reading or writing the same
// file does not block us. Only Windows has the sharing-violation behaviour that
// openLogFile retries for.
func transientOpenError(error) bool { return false }
