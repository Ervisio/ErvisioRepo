package blob

import (
	"fmt"
	"strings"
)

// MaxKeyLen is the longest accepted key, in bytes.
const MaxKeyLen = 1024

// ReservedPrefix starts the names of driver-internal files (temporary uploads,
// locks). Key segments may not start with '.', so they never collide.
const ReservedPrefix = ".erepo-"

// ValidateKey checks that key is a relative slash-separated path made of
// segments of [A-Za-z0-9._=+@,-], none empty, none starting with '.'.
// This keeps keys portable across S3, local filesystems and SFTP, and makes
// path traversal impossible by construction.
func ValidateKey(key string) error {
	if key == "" {
		return fmt.Errorf("%w: empty", ErrInvalidKey)
	}
	if len(key) > MaxKeyLen {
		return fmt.Errorf("%w: longer than %d bytes", ErrInvalidKey, MaxKeyLen)
	}
	for seg := range strings.SplitSeq(key, "/") {
		if err := validateSegment(seg); err != nil {
			return fmt.Errorf("%w: %q: %s", ErrInvalidKey, key, err.Error())
		}
	}
	return nil
}

// ValidatePrefix checks a List prefix: empty, or a key optionally followed by
// a single trailing '/'.
func ValidatePrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	return ValidateKey(strings.TrimSuffix(prefix, "/"))
}

type segError string

func (e segError) Error() string { return string(e) }

func validateSegment(seg string) error {
	if seg == "" {
		return segError("empty segment")
	}
	if seg[0] == '.' {
		return segError("segment starts with '.'")
	}
	for i := 0; i < len(seg); i++ {
		if !keyByte(seg[i]) {
			return segError(fmt.Sprintf("invalid byte 0x%02x", seg[i]))
		}
	}
	return nil
}

func keyByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '.', '_', '-', '=', '+', '@', ',':
		return true
	}
	return false
}
