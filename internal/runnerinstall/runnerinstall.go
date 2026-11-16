// Package runnerinstall serves the ervisio-runner install scripts at
// /runner/install.sh and /runner/install.ps1. The server templates its own
// version and public URL into them; they download the matching release
// assets from GitHub and verify their checksums.
package runnerinstall

import (
	_ "embed"
	"net/http"
	"regexp"
	"strings"
)

var (
	//go:embed install.sh
	installSh string
	//go:embed install.ps1
	installPs1 string
)

// Paths of the scripts.
const (
	ShellPath      = "/runner/install.sh"
	PowerShellPath = "/runner/install.ps1"
)

var (
	versionRe = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	urlRe     = regexp.MustCompile(`^https?://[A-Za-z0-9.:\[\]_-]+(/[A-Za-z0-9._~/-]*)?$`)
)

// render fills the placeholders. A development version becomes "latest"
// and the script resolves the latest release; an
// unexpected URL is left out so nothing unsafe reaches the shell.
func render(script, version, publicURL string) string {
	version = strings.TrimPrefix(version, "v")
	if !versionRe.MatchString(version) {
		version = "latest"
	}
	publicURL = strings.TrimRight(publicURL, "/")
	if !urlRe.MatchString(publicURL) {
		publicURL = ""
	}
	return strings.NewReplacer("@@VERSION@@", version, "@@URL@@", publicURL).Replace(script)
}

// Handler serves one script; it needs no authentication.
func Handler(script, contentType, version, publicURL string) http.HandlerFunc {
	body := []byte(render(script, version, publicURL))
	return func(w http.ResponseWriter, _ *http.Request) {
		h := w.Header()
		h.Set("Content-Type", contentType)
		h.Set("Cache-Control", "no-cache")
		h.Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(body)
	}
}

// Shell serves install.sh.
func Shell(version, publicURL string) http.HandlerFunc {
	return Handler(installSh, "text/x-shellscript; charset=utf-8", version, publicURL)
}

// PowerShell serves install.ps1.
func PowerShell(version, publicURL string) http.HandlerFunc {
	return Handler(installPs1, "text/plain; charset=utf-8", version, publicURL)
}
