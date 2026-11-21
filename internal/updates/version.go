package updates

import (
	"fmt"
	"strconv"
	"strings"
)

// version is a release version x.y.z.
type version [3]int

// parseVersion reads "x.y.z" with an optional "v" prefix; pre-release and
// build suffixes are not release versions.
func parseVersion(s string) (version, bool) {
	var v version
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(s), "v"), ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		if p == "" || len(p) > 6 {
			return v, false
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

func (v version) String() string { return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2]) }

// less reports v < o.
func (v version) less(o version) bool {
	for i := range v {
		if v[i] != o[i] {
			return v[i] < o[i]
		}
	}
	return false
}

// Normalize returns s as "x.y.z", or "" when it is not a release version.
func Normalize(s string) string {
	v, ok := parseVersion(s)
	if !ok {
		return ""
	}
	return v.String()
}

// Newer reports whether b is a newer release than a; false when either is
// not a release version.
func Newer(a, b string) bool {
	va, ok1 := parseVersion(a)
	vb, ok2 := parseVersion(b)
	return ok1 && ok2 && va.less(vb)
}
