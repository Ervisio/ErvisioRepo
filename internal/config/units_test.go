package config

import (
	"testing"
	"time"
)

func TestParseSize(t *testing.T) {
	tests := []struct {
		in   string
		want Size
		ok   bool
	}{
		{"50GB", 50e9, true},
		{"50gb", 50e9, true},
		{"512MB", 512e6, true},
		{"1.5GiB", 3 << 29, true},
		{"10KiB", 10 << 10, true},
		{"0B", 0, true},
		{" 2 TB ", 2e12, true},
		{"50", 0, false},
		{"GB", 0, false},
		{"-1GB", 0, false},
		{"1e3GB", 0, false},
		{"1..2GB", 0, false},
		{"99999999TB", 0, false},
	}
	for _, tt := range tests {
		got, err := ParseSize(tt.in)
		if (err == nil) != tt.ok || (tt.ok && got != tt.want) {
			t.Errorf("ParseSize(%q) = %d, %v; want %d ok=%v", tt.in, got, err, tt.want, tt.ok)
		}
	}
}

func TestParseDuration(t *testing.T) {
	day := 24 * time.Hour
	tests := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"7d", 7 * day, true},
		{"2w", 14 * day, true},
		{"12h", 12 * time.Hour, true},
		{"1h30m", 90 * time.Minute, true},
		{"0s", 0, true},
		{"d", 0, false},
		{"1.5d", 0, false},
		{"-1h", 0, false},
		{"-1d", 0, false},
		{"7days", 0, false},
		{"999999999999d", 0, false},
	}
	for _, tt := range tests {
		got, err := ParseDuration(tt.in)
		if (err == nil) != tt.ok || (tt.ok && time.Duration(got) != tt.want) {
			t.Errorf("ParseDuration(%q) = %v, %v; want %v ok=%v", tt.in, got, err, tt.want, tt.ok)
		}
	}
}

func FuzzParseSize(f *testing.F) {
	for _, s := range []string{"50GB", "1.5GiB", "0B", "7TB", "1KB", ".5MB", "x"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		v, err := ParseSize(s)
		if err != nil {
			return
		}
		if v < 0 {
			t.Fatalf("ParseSize(%q) = %d < 0", s, v)
		}
		back, err := ParseSize(v.String())
		if err != nil || back != v {
			t.Fatalf("round trip %q -> %q -> %d, %v", s, v.String(), back, err)
		}
	})
}

func FuzzParseDuration(f *testing.F) {
	for _, s := range []string{"7d", "2w", "1h30m", "0s", "90s", "-1h", "1.5h"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		d, err := ParseDuration(s)
		if err != nil {
			return
		}
		if d < 0 {
			t.Fatalf("ParseDuration(%q) = %v < 0", s, d)
		}
		back, err := ParseDuration(d.String())
		if err != nil || back != d {
			t.Fatalf("round trip %q -> %q -> %v, %v", s, d.String(), back, err)
		}
	})
}
