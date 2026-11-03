package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestResolve(t *testing.T) {
	withVCS := &debug.BuildInfo{
		Main:     debug.Module{Version: "v1.2.3"},
		Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "deadbeef"}},
	}
	devel := &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}

	tests := []struct {
		name             string
		ldVer, ldCommit  string
		bi               *debug.BuildInfo
		wantVer, wantCmt string
	}{
		{"ldflags win", "v0.1.0", "abc", withVCS, "v0.1.0", "abc"},
		{"fallback to build info", "", "", withVCS, "v1.2.3", "deadbeef"},
		{"devel build", "", "", devel, "dev", ""},
		{"no build info", "", "", nil, "dev", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolve(tt.ldVer, tt.ldCommit, tt.bi)
			if got.Version != tt.wantVer || got.Commit != tt.wantCmt {
				t.Fatalf("got %+v, want version=%q commit=%q", got, tt.wantVer, tt.wantCmt)
			}
		})
	}
}

func TestGetNeverEmpty(t *testing.T) {
	if Get().Version == "" {
		t.Fatal("Version must never be empty")
	}
}
