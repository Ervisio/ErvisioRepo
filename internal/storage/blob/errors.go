package blob

import (
	"context"
	"errors"
	"io"
	"net"
	"syscall"
)

// transientError marks an error as worth retrying.
type transientError struct{ err error }

func (e *transientError) Error() string   { return e.err.Error() }
func (e *transientError) Unwrap() error   { return e.err }
func (e *transientError) Transient() bool { return true }

// MarkTransient wraps err so that IsTransient reports true. nil stays nil.
func MarkTransient(err error) error {
	if err == nil {
		return nil
	}
	return &transientError{err: err}
}

// IsTransient reports whether err is a temporary failure (network trouble,
// throttling, server errors) that a retry may fix. Contract errors (the
// sentinels of this package) and cancellations are never transient.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	for _, perm := range []error{
		ErrNotFound, ErrPreconditionFailed, ErrInvalidKey, ErrInvalidRange,
		ErrSizeMismatch, ErrChecksumMismatch, ErrNotSupported,
		context.Canceled, context.DeadlineExceeded,
	} {
		if errors.Is(err, perm) {
			return false
		}
	}
	var t interface{ Transient() bool }
	if errors.As(err, &t) {
		return t.Transient()
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	return errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNABORTED) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ETIMEDOUT)
}
