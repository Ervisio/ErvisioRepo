package backup

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"golang.org/x/crypto/argon2"
)

// The recovery kit holds what no backup contains and a new server needs:
// the configuration file, the master key (without it the encrypted targets
// are unreadable) and the SSH host keys (without them every client warns
// of a changed host). It is sealed with AES-256-GCM under a key derived
// from a passphrase with argon2id.
//
// Layout: magic | time u32 | memory KiB u32 | threads u8 | salt 16 |
// nonce 12 | ciphertext. The header is the GCM additional data.

var kitMagic = []byte("ERVISIO-KIT1\n")

// Default argon2id parameters (RFC 9106 second recommendation, stronger
// time cost: the kit is opened once, offline).
const (
	kitTime    = 4
	kitMemory  = 64 * 1024
	kitThreads = 4
	kitSaltLen = 16
	kitKeyLen  = 32
	// kitMaxMemory bounds the memory an imported kit can ask for (1 GiB).
	kitMaxMemory = 1 << 20
	// MinPassphrase is the shortest passphrase accepted on export.
	MinPassphrase = 12
)

// ErrBadKit is returned for a kit that is not one, is damaged or was sealed
// with another passphrase (the two cannot be told apart).
var ErrBadKit = errors.New("backup: invalid recovery kit or wrong passphrase")

// KitFile is one file of the kit, restored at Path with Mode.
type KitFile struct {
	Path string      `json:"path"`
	Mode fs.FileMode `json:"mode"`
	Data []byte      `json:"data"`
}

// Kit is the decrypted content of a recovery kit.
type Kit struct {
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	Config    *KitFile  `json:"config,omitempty"`
	MasterKey *KitFile  `json:"master_key,omitempty"`
	HostKeys  []KitFile `json:"host_keys,omitempty"`
}

// Files returns every file of the kit.
func (k *Kit) Files() []KitFile {
	var out []KitFile
	for _, f := range []*KitFile{k.Config, k.MasterKey} {
		if f != nil {
			out = append(out, *f)
		}
	}
	return append(out, k.HostKeys...)
}

// KitSources says which files go into a kit.
type KitSources struct {
	ConfigFile    string // may be empty
	MasterKeyFile string // may be empty
	HostKeyDir    string // every regular file in it; may be empty or missing
}

// CollectKit reads the files of s.
func CollectKit(s KitSources, now time.Time) (*Kit, error) {
	k := &Kit{Version: 1, CreatedAt: now.UTC()}
	read := func(path string) (*KitFile, error) {
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("kit: %w", err)
		}
		fi, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("kit: %w", err)
		}
		data, err := os.ReadFile(abs) //nolint:gosec // operator-supplied path
		if err != nil {
			return nil, fmt.Errorf("kit: %w", err)
		}
		return &KitFile{Path: abs, Mode: fi.Mode().Perm(), Data: data}, nil
	}
	var err error
	if s.ConfigFile != "" {
		if k.Config, err = read(s.ConfigFile); err != nil {
			return nil, err
		}
	}
	if s.MasterKeyFile != "" {
		if k.MasterKey, err = read(s.MasterKeyFile); err != nil {
			return nil, err
		}
	}
	if s.HostKeyDir != "" {
		entries, err := os.ReadDir(s.HostKeyDir)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("kit: %w", err)
		}
		for _, e := range entries {
			if !e.Type().IsRegular() {
				continue
			}
			f, err := read(filepath.Join(s.HostKeyDir, e.Name()))
			if err != nil {
				return nil, err
			}
			k.HostKeys = append(k.HostKeys, *f)
		}
	}
	if len(k.Files()) == 0 {
		return nil, errors.New("kit: nothing to export")
	}
	return k, nil
}

// SealKit encrypts k under passphrase and writes it to w.
func SealKit(w io.Writer, k *Kit, passphrase []byte) error {
	if len(passphrase) < MinPassphrase {
		return fmt.Errorf("kit: the passphrase must be at least %d characters", MinPassphrase)
	}
	plain, err := json.Marshal(k)
	if err != nil {
		return fmt.Errorf("kit: %w", err)
	}
	salt := make([]byte, kitSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("kit: %w", err)
	}
	var hdr bytes.Buffer
	hdr.Write(kitMagic)
	_ = binary.Write(&hdr, binary.BigEndian, uint32(kitTime))
	_ = binary.Write(&hdr, binary.BigEndian, uint32(kitMemory))
	hdr.WriteByte(kitThreads)
	hdr.Write(salt)
	aead, err := kitAEAD(passphrase, salt, kitTime, kitMemory, kitThreads)
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("kit: %w", err)
	}
	hdr.Write(nonce)
	header := hdr.Bytes()
	out := aead.Seal(slices.Clone(header), nonce, plain, header)
	if _, err := w.Write(out); err != nil {
		return fmt.Errorf("kit: %w", err)
	}
	return nil
}

// OpenKit decrypts a kit read from r.
func OpenKit(r io.Reader, passphrase []byte) (*Kit, error) {
	data, err := io.ReadAll(io.LimitReader(r, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("kit: %w", err)
	}
	hlen := len(kitMagic) + 4 + 4 + 1 + kitSaltLen + 12
	if len(data) < hlen || !bytes.Equal(data[:len(kitMagic)], kitMagic) {
		return nil, ErrBadKit
	}
	p := data[len(kitMagic):]
	t := binary.BigEndian.Uint32(p[0:4])
	m := binary.BigEndian.Uint32(p[4:8])
	threads := p[8]
	salt := p[9 : 9+kitSaltLen]
	nonce := p[9+kitSaltLen : 9+kitSaltLen+12]
	if t == 0 || t > 64 || m < 8*uint32(max(threads, 1)) || m > kitMaxMemory || threads == 0 {
		return nil, ErrBadKit
	}
	aead, err := kitAEAD(passphrase, salt, t, m, threads)
	if err != nil {
		return nil, err
	}
	plain, err := aead.Open(nil, nonce, data[hlen:], data[:hlen])
	if err != nil {
		return nil, ErrBadKit
	}
	var k Kit
	if err := json.Unmarshal(plain, &k); err != nil || k.Version != 1 {
		return nil, ErrBadKit
	}
	return &k, nil
}

func kitAEAD(passphrase, salt []byte, t, m uint32, threads uint8) (cipher.AEAD, error) {
	key := argon2.IDKey(passphrase, salt, t, m, threads, kitKeyLen)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("kit: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("kit: %w", err)
	}
	return aead, nil
}

// InstallKit writes the files of k to their recorded paths, or under root
// (keeping the absolute layout) when root is set. An existing file with
// different content is an error unless overwrite is set.
func InstallKit(k *Kit, root string, overwrite bool) ([]string, error) {
	var written []string
	for _, f := range k.Files() {
		if !filepath.IsAbs(f.Path) || filepath.Clean(f.Path) != f.Path {
			return written, fmt.Errorf("kit: unsafe path %q", f.Path)
		}
		dst := f.Path
		if root != "" {
			dst = filepath.Join(root, f.Path)
		}
		if old, err := os.ReadFile(dst); err == nil { //nolint:gosec // path from the sealed kit
			if bytes.Equal(old, f.Data) {
				continue
			}
			if !overwrite {
				return written, fmt.Errorf("kit: %s exists with different content (use --force)", dst)
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return written, fmt.Errorf("kit: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return written, fmt.Errorf("kit: %w", err)
		}
		mode := f.Mode.Perm() & 0o600
		if mode == 0 {
			mode = 0o600
		}
		if err := os.WriteFile(dst, f.Data, mode); err != nil {
			return written, fmt.Errorf("kit: %w", err)
		}
		written = append(written, dst)
	}
	return written, nil
}
