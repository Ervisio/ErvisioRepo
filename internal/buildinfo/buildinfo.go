// Package buildinfo exposes the version and VCS revision of the running binary.
//
// Release builds set them with:
//
//	-ldflags "-X github.com/Ervisio/ErvisioRepo/internal/buildinfo.version=v0.1.0
//	          -X github.com/Ervisio/ErvisioRepo/internal/buildinfo.commit=abc1234"
//
// Otherwise they fall back to the information embedded by the Go toolchain.
package buildinfo

import (
	"runtime/debug"
	"sync"
)

// Set via -ldflags.
var (
	version string
	commit  string
)

// Info describes the running binary.
type Info struct {
	Version string
	Commit  string // empty when unknown
}

var load = sync.OnceValue(func() Info {
	bi, _ := debug.ReadBuildInfo()
	return resolve(version, commit, bi)
})

// Get returns the build information of the running binary.
func Get() Info { return load() }

func resolve(ldVersion, ldCommit string, bi *debug.BuildInfo) Info {
	info := Info{Version: ldVersion, Commit: ldCommit}
	if bi != nil {
		if info.Version == "" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			info.Version = bi.Main.Version
		}
		if info.Commit == "" {
			for _, s := range bi.Settings {
				if s.Key == "vcs.revision" {
					info.Commit = s.Value
				}
			}
		}
	}
	if info.Version == "" {
		info.Version = "dev"
	}
	return info
}
