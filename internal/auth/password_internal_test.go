package auth

import (
	"strings"
	"testing"
)

func TestDecodePHC(t *testing.T) {
	good := "$argon2id$v=19$m=64,t=1,p=1$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5a2V5a2V5"
	p, salt, key, err := decodePHC(good)
	if err != nil || p.MemoryKiB != 64 || p.Iterations != 1 || p.Parallelism != 1 || len(salt) != 12 || len(key) != 18 {
		t.Fatalf("decode: %+v %d %d %v", p, len(salt), len(key), err)
	}
	if encodePHC(p, salt, key) != good {
		t.Fatal("round trip")
	}
	for _, bad := range []string{
		"", "$argon2i$v=19$m=64,t=1,p=1$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=16$m=64,t=1,p=1$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=64,t=0,p=1$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=4,t=1,p=1$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=99999999,t=1,p=1$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=64,t=101,p=1$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=64,t=1,p=0$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=64,t=1,p=1$c2Fs$a2V5a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=64,t=1,p=1$c2FsdHNhbHRzYWx0$a2V5",
		"$argon2id$v=19$m=64,t=1,p=1$!!!$a2V5a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=64,t=1,p=1$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5a2V5a2V5$extra",
	} {
		if _, _, _, err := decodePHC(bad); err != ErrMalformedHash {
			t.Errorf("decodePHC(%q) = %v", bad, err)
		}
	}
}

// FuzzDecodePHC: the parser never panics and only accepts bounded costs, so
// a corrupted stored hash cannot make Verify allocate without limit.
func FuzzDecodePHC(f *testing.F) {
	f.Add("$argon2id$v=19$m=64,t=1,p=1$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5a2V5a2V5")
	f.Add("$argon2id$v=19$m=65536,t=3,p=2$AAAAAAAAAAAAAAAAAAAAAA$" + strings.Repeat("A", 43))
	f.Add("$$$$$")
	f.Fuzz(func(t *testing.T, s string) {
		p, salt, key, err := decodePHC(s)
		if err != nil {
			return
		}
		if p.MemoryKiB > 4<<20 || p.Iterations > 100 || p.Parallelism < 1 || len(salt) > 64 || len(key) > 128 {
			t.Fatalf("accepted unbounded parameters %+v", p)
		}
	})
}
