package storagecfg

import (
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"

	"golang.org/x/crypto/ssh"

	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
	"github.com/Ervisio/ErvisioRepo/internal/storage/tier"
)

// Limits of the text fields.
const (
	maxEndpoint   = 512
	maxPrefix     = 256
	maxRoot       = 255
	maxKeyText    = 16 << 10
	maxHostsText  = 64 << 10
	maxCredential = 512
	defaultPort   = 22
	defaultConns  = 4
	maxConnsLimit = 64
)

var (
	nameRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	bucketRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
	regionRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)
	hostRe   = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)
	userRe   = regexp.MustCompile(`^[A-Za-z0-9._][A-Za-z0-9._-]{0,63}$`)
)

// FieldError is one rejected input field.
type FieldError struct {
	Path string
	Msg  string
}

// ValidationError lists what an operation was refused for.
type ValidationError struct {
	Detail string
	Errors []FieldError
}

func (e *ValidationError) Error() string {
	if e.Detail != "" {
		return e.Detail
	}
	return "invalid storage configuration"
}

func fieldErr(path, format string, args ...any) *ValidationError {
	return &ValidationError{Errors: []FieldError{{Path: path, Msg: fmt.Sprintf(format, args...)}}}
}

// Secrets are the credentials typed in the form. Empty means "not provided":
// a stored value is kept. They are write-only: no function returns them.
type Secrets struct {
	AccessKey  string
	SecretKey  string
	PrivateKey string
	Passphrase string
	KnownHosts string
	// Password is the SFTP account password, an alternative to PrivateKey.
	Password string
}

// TargetInput is the definition of an s3 or sftp target as sent by the form.
type TargetInput struct {
	Name    string
	Kind    string
	Encrypt bool

	Endpoint  string
	Bucket    string
	Region    string
	PathStyle bool
	Prefix    string

	Host     string
	Port     int
	User     string
	Root     string
	MaxConns int

	Secrets Secrets
}

// PolicyInput is the new placement of a data type.
type PolicyInput struct {
	Primary  string
	Replicas []string
	// Cache is a local target, config.CacheNone, or empty for the automatic
	// hot cache.
	Cache string
	// CacheTTL, when set, is the idle TTL of this data type (0: the hot
	// cache default); nil keeps the one of config.toml.
	CacheTTL *time.Duration
}

type problems []FieldError

func (p *problems) add(path, format string, args ...any) {
	*p = append(*p, FieldError{Path: path, Msg: fmt.Sprintf(format, args...)})
}

func (p problems) err() error {
	if len(p) == 0 {
		return nil
	}
	return &ValidationError{Errors: p}
}

// normalize trims the fields of in and applies the defaults, then checks
// every field. needName is false for a connection test of a new draft, which
// may leave the name empty. The credentials are checked for shape only.
func (in *TargetInput) normalize(needName bool) error {
	var p problems
	in.Name, in.Kind = strings.TrimSpace(in.Name), strings.TrimSpace(in.Kind)
	// A connection test of a new draft may leave the name empty.
	if (in.Name != "" || needName) && !nameRe.MatchString(in.Name) {
		p.add("name", "must start with a letter or digit and contain only lowercase letters, digits, '_' and '-' (up to 63 characters)")
	}
	in.Endpoint, in.Bucket, in.Region = strings.TrimSpace(in.Endpoint), strings.TrimSpace(in.Bucket), strings.TrimSpace(in.Region)
	in.Prefix, in.Host, in.User, in.Root = strings.TrimSpace(in.Prefix), strings.TrimSpace(in.Host), strings.TrimSpace(in.User), strings.TrimSpace(in.Root)

	switch in.Kind {
	case KindS3:
		in.s3Fields(&p)
	case KindSFTP:
		in.sftpFields(&p)
	default:
		p.add("kind", "must be s3 or sftp")
	}
	in.Secrets.shape(&p)
	return p.err()
}

func (in *TargetInput) s3Fields(p *problems) {
	if in.Host != "" || in.Port != 0 || in.User != "" || in.Root != "" || in.MaxConns != 0 {
		p.add("kind", "SFTP fields are not used by an S3 target")
	}
	if in.Secrets.PrivateKey != "" || in.Secrets.Passphrase != "" || in.Secrets.KnownHosts != "" || in.Secrets.Password != "" {
		p.add("secrets", "SFTP credentials are not used by an S3 target")
	}
	switch u, err := url.Parse(in.Endpoint); {
	case in.Endpoint == "":
		p.add("endpoint", "is required")
	case len(in.Endpoint) > maxEndpoint:
		p.add("endpoint", "is too long")
	case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "":
		p.add("endpoint", "must be an absolute http(s) URL such as https://s3.example.com")
	case u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "":
		p.add("endpoint", "must not contain credentials, a query or a fragment")
	case !validHost(u.Hostname()):
		p.add("endpoint", "has an invalid host name")
	case blockedHost(u.Hostname()):
		p.add("endpoint", "points to a link-local, metadata, unspecified or multicast address, which is not allowed")
	}
	if !bucketRe.MatchString(in.Bucket) || strings.Contains(in.Bucket, "..") {
		p.add("bucket", "must be 3 to 63 characters: lowercase letters, digits, '.' and '-'")
	}
	if in.Region != "" && !regionRe.MatchString(in.Region) {
		p.add("region", "must contain only letters, digits, '.', '_' and '-'")
	}
	if in.Prefix != "" {
		if !strings.HasSuffix(in.Prefix, "/") {
			in.Prefix += "/"
		}
		if len(in.Prefix) > maxPrefix || strings.HasPrefix(in.Prefix, "/") || blob.ValidatePrefix(in.Prefix) != nil {
			p.add("prefix", "must be a relative path such as erepo/ (letters, digits, '.', '_', '-' and '/')")
		}
	}
}

func (in *TargetInput) sftpFields(p *problems) {
	if in.Endpoint != "" || in.Bucket != "" || in.Region != "" || in.Prefix != "" || in.PathStyle {
		p.add("kind", "S3 fields are not used by an SFTP target")
	}
	if in.Secrets.AccessKey != "" || in.Secrets.SecretKey != "" {
		p.add("secrets", "S3 credentials are not used by an SFTP target")
	}
	if !validHost(in.Host) {
		p.add("host", "must be a host name or an IP address")
	} else if blockedHost(in.Host) {
		p.add("host", "is a link-local, metadata, unspecified or multicast address, which is not allowed")
	}
	if in.Port == 0 {
		in.Port = defaultPort
	}
	if in.Port < 1 || in.Port > 65535 {
		p.add("port", "must be between 1 and 65535")
	}
	if !userRe.MatchString(in.User) {
		p.add("user", "must be a valid user name (letters, digits, '.', '_' and '-')")
	}
	if in.Root != "" && !validRemotePath(in.Root) {
		p.add("root", "must be a path without '..', control characters or backslashes (up to %d characters)", maxRoot)
	}
	if in.MaxConns == 0 {
		in.MaxConns = defaultConns
	}
	if in.MaxConns < 1 || in.MaxConns > maxConnsLimit {
		p.add("maxConns", "must be between 1 and %d", maxConnsLimit)
	}
}

// shape trims and bounds the credentials typed in the form.
func (s *Secrets) shape(p *problems) {
	for _, f := range []struct {
		path string
		v    *string
	}{{"secrets.accessKey", &s.AccessKey}, {"secrets.secretKey", &s.SecretKey}} {
		*f.v = strings.TrimSpace(*f.v)
		if *f.v == "" {
			continue
		}
		if len(*f.v) > maxCredential || strings.IndexFunc(*f.v, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
			p.add(f.path, "must not contain spaces or control characters (up to %d characters)", maxCredential)
		}
	}
	if s.PrivateKey != "" {
		s.PrivateKey = strings.TrimSpace(strings.ReplaceAll(s.PrivateKey, "\r\n", "\n")) + "\n"
		if len(s.PrivateKey) > maxKeyText {
			p.add("secrets.privateKey", "is too long")
		}
	}
	if len(s.Passphrase) > maxCredential {
		p.add("secrets.passphrase", "is too long")
	}
	if len(s.Password) > maxCredential || strings.IndexFunc(s.Password, unicode.IsControl) >= 0 {
		p.add("secrets.password", "must not contain control characters (up to %d characters)", maxCredential)
	}
	if s.KnownHosts != "" {
		s.KnownHosts = strings.TrimSpace(strings.ReplaceAll(s.KnownHosts, "\r\n", "\n")) + "\n"
		if len(s.KnownHosts) > maxHostsText {
			p.add("secrets.knownHosts", "is too long")
		}
	}
}

// checkSFTPSecrets parses the key and the known hosts of an SFTP target in
// clear text. The messages never contain key material.
func checkSFTPSecrets(key, passphrase, hosts string, p *problems) {
	if key != "" {
		if _, err := tier.ParsePrivateKey([]byte(key), []byte(passphrase)); err != nil {
			var missing *ssh.PassphraseMissingError
			switch {
			case errors.As(err, &missing):
				p.add("secrets.passphrase", "is required: the private key is protected by a passphrase")
			case errors.Is(err, x509.IncorrectPasswordError):
				p.add("secrets.passphrase", "does not open the private key")
			case passphrase != "" && unprotected(key):
				p.add("secrets.passphrase", "is not needed: the private key is not protected by a passphrase")
			default:
				p.add("secrets.privateKey", "is not a valid private key in OpenSSH or PEM format")
			}
		}
	}
	if hosts != "" {
		if !hasHostLine(hosts) {
			p.add("secrets.knownHosts", "must contain at least one host key line (OpenSSH known_hosts format)")
		} else if _, err := tier.HostKeyCallback([]byte(hosts)); err != nil {
			p.add("secrets.knownHosts", "is not valid known_hosts content")
		}
	}
}

// unprotected reports whether the key parses without a passphrase.
func unprotected(key string) bool {
	_, err := tier.ParsePrivateKey([]byte(key), nil)
	return err == nil
}

// blockedHost reports whether h is an IP literal that targets may not use.
func blockedHost(h string) bool {
	ip := net.ParseIP(h)
	return ip != nil && tier.BlockedAddress(ip)
}

func hasHostLine(s string) bool {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			return true
		}
	}
	return false
}

func validHost(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	return net.ParseIP(h) != nil || hostRe.MatchString(h)
}

func validRemotePath(s string) bool {
	if len(s) > maxRoot || strings.ContainsRune(s, '\\') {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	for _, seg := range strings.Split(s, "/") {
		if seg == ".." {
			return false
		}
	}
	return true
}
