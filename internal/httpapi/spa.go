package httpapi

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"io/fs"
	"net/http"
	"path"
	"strconv"
	"strings"
)

// cspNoncePlaceholder is written into index.html by Vite (html.cspNonce in
// web/vite.config.ts) and replaced with a fresh nonce on every response.
const cspNoncePlaceholder = "__EREPO_CSP_NONCE__"

// spaContentSecurityPolicy is contentSecurityPolicy for index.html: style-src
// also accepts the response nonce, which the SPA hands to the libraries that
// inject <style> tags at runtime (Radix scroll lock and select viewport).
// Scripts stay limited to 'self', and inline style attributes in markup stay
// blocked; styles set through the CSSOM (React style props) are not subject
// to style-src.
func spaContentSecurityPolicy(nonce string) string {
	return strings.Replace(contentSecurityPolicy, "style-src 'self'", "style-src 'self' 'nonce-"+nonce+"'", 1)
}

func newNonce() (string, error) {
	var b [18]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b[:]), nil
}

const missingSPA = "ErvisioRepo: the web UI has not been built.\n\n" +
	"Run `make web` and rebuild the server, or use the Vite dev server (`make dev`).\n"

// spaHandler serves the embedded SPA: existing files as they are, and
// index.html for every other path without an extension so that client-side
// routes work on reload.
func spaHandler(fsys fs.FS, hasIndex bool) http.Handler {
	if !hasIndex {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write([]byte(missingSPA))
		})
	}
	files := http.FileServerFS(fsys)
	index, indexErr := fs.ReadFile(fsys, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		if containsDotDot(r.URL.Path) {
			http.Error(w, "invalid URL path", http.StatusBadRequest)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name != "" && name != "index.html" {
			if st, err := fs.Stat(fsys, name); err == nil && !st.IsDir() {
				if strings.HasPrefix(name, "assets/") {
					// Vite puts content hashes in asset file names.
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
			if path.Ext(name) != "" {
				http.NotFound(w, r)
				return
			}
		}
		serveIndex(w, r, index, indexErr)
	})
}

// serveIndex writes index.html with a per-response CSP nonce.
func serveIndex(w http.ResponseWriter, r *http.Request, index []byte, readErr error) {
	if readErr != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	h := w.Header()
	// Revalidate on every load; without validators that refetches the page
	// and so a fresh nonce.
	h.Set("Cache-Control", "no-cache")
	body := index
	if bytes.Contains(index, []byte(cspNoncePlaceholder)) {
		nonce, err := newNonce()
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		body = bytes.ReplaceAll(index, []byte(cspNoncePlaceholder), []byte(nonce))
		h.Set("Content-Security-Policy", spaContentSecurityPolicy(nonce))
	}
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

// containsDotDot reports whether p has a ".." segment (as net/http does).
func containsDotDot(p string) bool {
	if !strings.Contains(p, "..") {
		return false
	}
	for _, seg := range strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." {
			return true
		}
	}
	return false
}
