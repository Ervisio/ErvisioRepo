package config

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Size is a number of bytes written as "512MB", "50GB" or "1.5GiB".
// Units: B, KB, MB, GB, TB (powers of 1000) and KiB, MiB, GiB, TiB (powers of 1024).
type Size int64

var sizeUnits = []struct {
	suffix string
	mult   int64
}{
	// Longest suffixes first so that "GiB" is not read as "B".
	{"KIB", 1 << 10}, {"MIB", 1 << 20}, {"GIB", 1 << 30}, {"TIB", 1 << 40},
	{"KB", 1e3}, {"MB", 1e6}, {"GB", 1e9}, {"TB", 1e12},
	{"B", 1},
}

// ParseSize parses a human-readable size.
func ParseSize(s string) (Size, error) {
	in := strings.ToUpper(strings.TrimSpace(s))
	for _, u := range sizeUnits {
		num, ok := strings.CutSuffix(in, u.suffix)
		if !ok {
			continue
		}
		n, err := parseDecimal(strings.TrimSpace(num))
		if err != nil {
			return 0, fmt.Errorf("invalid size %q: %w", s, err)
		}
		v := n * float64(u.mult)
		if v >= math.MaxInt64 {
			return 0, fmt.Errorf("invalid size %q: too large", s)
		}
		return Size(v), nil
	}
	return 0, fmt.Errorf("invalid size %q: missing unit (B, KB, MB, GB, TB, KiB, MiB, GiB, TiB)", s)
}

// parseDecimal accepts only plain non-negative decimals such as "50" or "1.5".
func parseDecimal(s string) (float64, error) {
	if s == "" || strings.Trim(s, "0123456789.") != "" || strings.Count(s, ".") > 1 || s == "." {
		return 0, errors.New("expected a non-negative number")
	}
	return strconv.ParseFloat(s, 64)
}

func (s Size) String() string {
	for _, u := range []struct {
		suffix string
		mult   int64
	}{{"TB", 1e12}, {"GB", 1e9}, {"MB", 1e6}, {"KB", 1e3}} {
		if s != 0 && int64(s)%u.mult == 0 {
			return strconv.FormatInt(int64(s)/u.mult, 10) + u.suffix
		}
	}
	return strconv.FormatInt(int64(s), 10) + "B"
}

func (s *Size) UnmarshalText(b []byte) error {
	v, err := ParseSize(string(b))
	if err != nil {
		return err
	}
	*s = v
	return nil
}

func (s Size) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// Duration is a time.Duration that also accepts days and weeks ("7d", "2w").
type Duration time.Duration

// ParseDuration parses "7d", "2w" or anything time.ParseDuration accepts.
// Negative durations are rejected.
func ParseDuration(s string) (Duration, error) {
	in := strings.TrimSpace(s)
	unit := time.Duration(0)
	switch {
	case strings.HasSuffix(in, "d"):
		unit = 24 * time.Hour
	case strings.HasSuffix(in, "w"):
		unit = 7 * 24 * time.Hour
	}
	if unit != 0 {
		num := in[:len(in)-1]
		if num == "" || strings.Trim(num, "0123456789") != "" {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		n, err := strconv.ParseInt(num, 10, 64)
		if err != nil || n > math.MaxInt64/int64(unit) {
			return 0, fmt.Errorf("invalid duration %q: out of range", s)
		}
		return Duration(time.Duration(n) * unit), nil
	}
	d, err := time.ParseDuration(in)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", s, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("invalid duration %q: must not be negative", s)
	}
	return Duration(d), nil
}

func (d Duration) String() string {
	const day = 24 * time.Hour
	if td := time.Duration(d); td != 0 && td%day == 0 {
		return strconv.FormatInt(int64(td/day), 10) + "d"
	}
	return time.Duration(d).String()
}

func (d *Duration) UnmarshalText(b []byte) error {
	v, err := ParseDuration(string(b))
	if err != nil {
		return err
	}
	*d = v
	return nil
}

func (d Duration) MarshalText() ([]byte, error) { return []byte(d.String()), nil }
