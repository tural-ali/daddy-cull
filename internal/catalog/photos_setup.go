package catalog

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"
	"time"
)

// Cull Sync used to be set up by copying a key by hand, from the server's
// environment into a file on the Mac. Now the page asks for a one-time setup
// code and shows one command to paste into Terminal. The command fetches an
// installer with a fresh key inside, and that fetch burns the code, so the key
// is handed out exactly once and never appears in a URL, a page or a log.
//
// Unless PHOTOS_AGENT_KEY is set, the server keeps only the key's SHA-256, in
// the catalogue's settings, so a copy of library.db lets nobody in. Handing
// out a new key replaces the old one: there is one Photos library and so one
// helper, and a Mac set up again simply carries on with the new key.
const (
	// Long enough to walk to the Mac and open Terminal; short enough that a
	// command left in a chat or on a clipboard is soon worthless.
	photosSetupLifetime = 15 * time.Minute
	// Each page load asks for one code, so a handful covers several tabs; the
	// oldest is dropped past this, which bounds the memory a busy page can use.
	photosSetupMax = 8
	// The SHA-256 of the key handed out last, hex encoded. The key itself is
	// never stored.
	photosKeySetting = "photos_agent_key_sha256"
)

var (
	// ErrPhotosSetupUnavailable means this binary was built without the
	// helper's sources, so there is nothing to install.
	ErrPhotosSetupUnavailable = errors.New("this server cannot install Cull Sync")
	// photosHostPattern accepts what a person types as a host: a name, an IPv4
	// address or a bracketed IPv6 address, with an optional port. The host is
	// written into the installer, so nothing else is let through.
	photosHostPattern = regexp.MustCompile(`^(?:[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?|\[[0-9A-Fa-f:.]{2,45}\])(?::[0-9]{1,5})?$`)
)

type photosSetup struct {
	// id names the code to the page that asked for it, so the page can follow
	// it without the code travelling again. It is not a secret.
	id string
	// The code itself is kept only as its hash.
	code             [sha256.Size]byte
	created, expires time.Time
	used             time.Time
}

// PhotosSetupView is what the page is told about a setup code.
type PhotosSetupView struct {
	// ID names the setup, to follow it at /api/photos/setup/{id}. It is not a
	// secret.
	ID string `json:"id"`
	// Command is only filled in when the code is first made.
	Command string `json:"command,omitempty"`
	// State is waiting (not used yet), expired, used (the installer was
	// fetched) or connected (Cull Sync has since been heard with a key).
	State string `json:"state"`
	// Expires is when the code stops working, 15 minutes after it was made,
	// in RFC 3339 UTC.
	Expires string `json:"expires"`
	// Now is the server's time, in RFC 3339 UTC, to count down to Expires.
	Now string `json:"now"`
}

// photosBaseOK holds a server address to a scheme and a plain host.
func photosBaseOK(base string) bool {
	host, ok := strings.CutPrefix(base, "http://")
	if !ok {
		host, ok = strings.CutPrefix(base, "https://")
	}
	return ok && photosHostPattern.MatchString(host)
}

// validPhotosKey rejects keys too short to be secret, and characters that
// would not survive a line in sync.conf: spaces are trimmed there, and a
// control character would end the line.
func validPhotosKey(key string) bool {
	if len(key) < PhotosAgentKeyMin {
		return false
	}
	for i := 0; i < len(key); i++ {
		if key[i] <= ' ' || key[i] > '~' {
			return false
		}
	}
	return true
}

// ValidPhotosAgentKey reports whether a PHOTOS_AGENT_KEY value will be used.
func ValidPhotosAgentKey(key string) bool { return validPhotosKey(key) }

// loadPhotosKeyHash reads the stored hash, if a key was ever handed out.
func (s *Store) loadPhotosKeyHash() ([sha256.Size]byte, bool) {
	var hash [sha256.Size]byte
	var value string
	if err := s.read.QueryRow("SELECT value FROM settings WHERE key=?", photosKeySetting).Scan(&value); err != nil {
		return hash, false
	}
	raw, err := hex.DecodeString(value)
	if err != nil || len(raw) != sha256.Size {
		return hash, false
	}
	copy(hash[:], raw)
	return hash, true
}

func (s *Store) savePhotosKeyHash(ctx context.Context, hash [sha256.Size]byte) error {
	_, err := s.write.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", photosKeySetting, hex.EncodeToString(hash[:]))
	return err
}

// SetHelper gives the hub the helper's sources: setup.sh and everything the
// Mac builds the app from, under CullSync/. Without it the page cannot offer a
// setup command.
func (h *PhotosHub) SetHelper(sources fs.FS) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.helper = sources
}

func randomToken(n int) string {
	random := make([]byte, n)
	if _, err := rand.Read(random); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(random)
}

// NewSetup makes a one-time setup code and the command that uses it. base is
// the scheme and host the page was loaded from, which is also how the Mac will
// reach this server.
func (h *PhotosHub) NewSetup(base string) (PhotosSetupView, error) {
	if !photosBaseOK(base) {
		return PhotosSetupView{}, photosInstallBadHost
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.helper == nil {
		return PhotosSetupView{}, ErrPhotosSetupUnavailable
	}
	now := h.now()
	h.pruneSetupsLocked(now)
	// 128 bits: nobody guesses it in fifteen minutes, or ever.
	code := randomToken(16)
	setup := &photosSetup{id: newPhotosJobID(), code: sha256.Sum256([]byte(code)), created: now, expires: now.Add(photosSetupLifetime)}
	h.setups = append(h.setups, setup)
	if len(h.setups) > photosSetupMax {
		h.setups = h.setups[len(h.setups)-photosSetupMax:]
	}
	view := h.setupViewLocked(setup, now)
	// -f: an error page is never handed to bash. -sS: quiet, but errors show.
	view.Command = fmt.Sprintf("curl -fsSL %s/api/photos/install/%s | bash", base, code)
	return view, nil
}

// Setup reports what became of a code, for the page to follow.
func (h *PhotosHub) Setup(id string) (PhotosSetupView, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	for _, setup := range h.setups {
		if setup.id == id {
			return h.setupViewLocked(setup, now), true
		}
	}
	return PhotosSetupView{}, false
}

func (h *PhotosHub) setupViewLocked(setup *photosSetup, now time.Time) PhotosSetupView {
	view := PhotosSetupView{ID: setup.id, Expires: setup.expires.UTC().Format(time.RFC3339), Now: now.UTC().Format(time.RFC3339)}
	// Connected means a request with a valid key arrived after the installer
	// was fetched. With a generated key that can only be the new helper, as
	// the old key stopped working at that moment. With PHOTOS_AGENT_KEY the key
	// does not change, so a helper that was already running can also count.
	switch {
	case !setup.used.IsZero() && h.agentKeyed.After(setup.used):
		view.State = "connected"
	case !setup.used.IsZero():
		view.State = "used"
	case !now.Before(setup.expires):
		view.State = "expired"
	default:
		view.State = "waiting"
	}
	return view
}

// pruneSetupsLocked forgets codes nobody can use any more. Each is kept for one
// more lifetime after it runs out, so the page that made it can still see it
// connect, or be told it expired.
func (h *PhotosHub) pruneSetupsLocked(now time.Time) {
	kept := h.setups[:0]
	for _, setup := range h.setups {
		if now.Sub(setup.expires) < photosSetupLifetime {
			kept = append(kept, setup)
		}
	}
	clear(h.setups[len(kept):])
	h.setups = kept
}

// photosInstallRefusal is the reason an install command did nothing. Each one
// is said to the person at the Mac, in Terminal.
type photosInstallRefusal string

func (r photosInstallRefusal) Error() string { return string(r) }

const (
	photosInstallUnknown  photosInstallRefusal = "this setup command has expired or has already been used. Copy a new one from the Apple Photos page in Daddy Cull and run that."
	photosInstallBusy     photosInstallRefusal = "Cull Sync is changing Photos right now. Wait until the Apple Photos page says it has finished, then copy a new command and run it."
	photosInstallBadHost  photosInstallRefusal = "the address in this command could not be used. Copy the command again from the Apple Photos page."
	photosInstallNotSaved photosInstallRefusal = "Daddy Cull could not save the new key. Try again in a moment with a new command from the Apple Photos page."
)

// Install redeems a setup code for the installer, with the key inside. The
// code is burned before anything is handed out, so two fetches of one command
// cannot both get a key. base is where the Mac reached this server, and so
// where Cull Sync is told to find it.
func (h *PhotosHub) Install(ctx context.Context, code, base string) ([]byte, error) {
	if !photosBaseOK(base) {
		return nil, photosInstallBadHost
	}
	sum := sha256.Sum256([]byte(code))
	h.mu.Lock()
	h.tickLocked()
	now := h.now()
	var setup *photosSetup
	for _, candidate := range h.setups {
		// Every code is compared, so the time taken says nothing about which.
		if subtle.ConstantTimeCompare(candidate.code[:], sum[:]) == 1 && candidate.used.IsZero() && now.Before(candidate.expires) {
			setup = candidate
		}
	}
	if setup == nil {
		h.mu.Unlock()
		return nil, photosInstallUnknown
	}
	// Reinstalling stops the running helper, and a new key would lock it out
	// halfway through reporting what it changed.
	if j := h.job; j != nil && (j.state == "applying" || j.state == "queued_apply") {
		h.mu.Unlock()
		return nil, photosInstallBusy
	}
	setup.used = now
	fixed, helper := h.fixedKey, h.helper
	h.mu.Unlock()

	payload, digest, err := h.helperPayload(helper)
	if err != nil {
		h.unburn(setup)
		return nil, err
	}
	key := fixed
	if key == "" {
		if key, err = h.rotateKey(ctx); err != nil {
			h.unburn(setup)
			return nil, photosInstallNotSaved
		}
	}
	return renderPhotosInstaller(helper, base, key, payload, digest)
}

// unburn gives a code back when nothing was handed out for it after all.
func (h *PhotosHub) unburn(setup *photosSetup) {
	h.mu.Lock()
	defer h.mu.Unlock()
	setup.used = time.Time{}
}

// rotateKey makes a new key, stores its hash and starts accepting it, which
// stops accepting the previous one. The lock is not held while the catalogue
// is written, so heartbeats do not wait; rotations are serialised instead, so
// the stored hash and the accepted one cannot end up different.
func (h *PhotosHub) rotateKey(ctx context.Context) (string, error) {
	h.rotating.Lock()
	defer h.rotating.Unlock()
	// 256 bits, as 43 URL-safe characters.
	key := randomToken(32)
	hash := sha256.Sum256([]byte(key))
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := h.s.savePhotosKeyHash(ctx, hash); err != nil {
		return "", err
	}
	h.mu.Lock()
	h.keyHash, h.enabled = hash, true
	h.mu.Unlock()
	return key, nil
}

// helperPayload packs the helper's sources into a gzipped tar once; they are
// in the binary and never change while it runs.
func (h *PhotosHub) helperPayload(helper fs.FS) ([]byte, string, error) {
	h.payloadOnce.Do(func() {
		h.payload, h.payloadErr = packPhotosHelper(helper)
		sum := sha256.Sum256(h.payload)
		h.payloadSum = hex.EncodeToString(sum[:])
	})
	return h.payload, h.payloadSum, h.payloadErr
}

// packPhotosHelper tars everything under CullSync/ except the installer
// itself. Timestamps and owners are fixed, so the same sources always give
// the same bytes and the same checksum.
func packPhotosHelper(helper fs.FS) ([]byte, error) {
	if helper == nil {
		return nil, ErrPhotosSetupUnavailable
	}
	var buffer bytes.Buffer
	zipped := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(zipped)
	files := 0
	err := fs.WalkDir(helper, "CullSync", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name == "CullSync/setup.sh" {
			return nil
		}
		header := &tar.Header{Name: name, ModTime: time.Unix(0, 0), Format: tar.FormatPAX}
		if entry.IsDir() {
			header.Typeflag, header.Name, header.Mode = tar.TypeDir, name+"/", 0o755
			return archive.WriteHeader(header)
		}
		body, err := fs.ReadFile(helper, name)
		if err != nil {
			return err
		}
		header.Typeflag, header.Size, header.Mode = tar.TypeReg, int64(len(body)), 0o644
		if strings.HasSuffix(name, ".sh") {
			header.Mode = 0o755
		}
		if err := archive.WriteHeader(header); err != nil {
			return err
		}
		files++
		_, err = archive.Write(body)
		return err
	})
	if err == nil && files == 0 {
		err = ErrPhotosSetupUnavailable
	}
	if err == nil {
		err = archive.Close()
	}
	if err == nil {
		err = zipped.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("pack the Cull Sync sources: %w", err)
	}
	return buffer.Bytes(), nil
}

// renderPhotosInstaller puts the address, the key and the sources in front of
// setup.sh. Each value is single-quoted for the shell, and the address was
// already held to the characters a host can have.
func renderPhotosInstaller(helper fs.FS, base, key string, payload []byte, digest string) ([]byte, error) {
	setup, err := fs.ReadFile(helper, "CullSync/setup.sh")
	if err != nil {
		return nil, fmt.Errorf("read setup.sh: %w", err)
	}
	body := string(setup)
	if strings.HasPrefix(body, "#!") {
		body = body[strings.IndexByte(body, '\n')+1:]
	}
	var out bytes.Buffer
	out.WriteString("#!/bin/bash\n")
	out.WriteString("# Cull Sync setup, served once by Daddy Cull. The key below is private;\n")
	out.WriteString("# do not save or share this script.\n")
	fmt.Fprintf(&out, "CULL_SYNC_URL=%s\n", shellQuote(base))
	fmt.Fprintf(&out, "CULL_SYNC_TOKEN=%s\n", shellQuote(key))
	fmt.Fprintf(&out, "CULL_SYNC_PAYLOAD_SHA256=%s\n", shellQuote(digest))
	out.WriteString("cull_sync_payload() {\n  base64 --decode <<'CULL_SYNC_PAYLOAD'\n")
	encoded := base64.StdEncoding.EncodeToString(payload)
	for len(encoded) > 76 {
		out.WriteString(encoded[:76])
		out.WriteByte('\n')
		encoded = encoded[76:]
	}
	out.WriteString(encoded)
	out.WriteString("\nCULL_SYNC_PAYLOAD\n}\n")
	out.WriteString(body)
	return out.Bytes(), nil
}

// photosRefusalScript is what a refused install command runs instead: it says
// why, in Terminal, and exits without touching anything.
func photosRefusalScript(reason photosInstallRefusal) []byte {
	return []byte("#!/bin/bash\nprintf '\\nCull Sync was not installed: %s\\n' " + shellQuote(string(reason)) + " >&2\nexit 1\n")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
