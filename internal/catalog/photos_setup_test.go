package catalog

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"daddy-cull/next/mac"
)

const fakeBase = "http://cull.example.test:8830"

var tokenLine = regexp.MustCompile(`(?m)^CULL_SYNC_TOKEN='([^']*)'$`)

// setupHub is a hub with no PHOTOS_AGENT_KEY and the real helper sources, on
// a clock the test moves.
func setupHub(t *testing.T, s *Store, key string) (*PhotosHub, *photosClock) {
	t.Helper()
	h := NewPhotosHub(s, key)
	h.SetHelper(mac.CullSync)
	clock := &photosClock{at: time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)}
	h.now = clock.now
	h.started = clock.at
	return h, clock
}

// codeOf takes the one-time code back out of a command.
func codeOf(t *testing.T, view PhotosSetupView) string {
	t.Helper()
	prefix := "curl -fsSL " + fakeBase + "/api/photos/install/"
	if !strings.HasPrefix(view.Command, prefix) || !strings.HasSuffix(view.Command, " | bash") {
		t.Fatalf("command %q", view.Command)
	}
	return strings.TrimSuffix(strings.TrimPrefix(view.Command, prefix), " | bash")
}

func install(t *testing.T, h *PhotosHub) (script []byte, token string, view PhotosSetupView) {
	t.Helper()
	view, err := h.NewSetup(fakeBase)
	if err != nil {
		t.Fatal(err)
	}
	script, err = h.Install(context.Background(), codeOf(t, view), fakeBase)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	match := tokenLine.FindSubmatch(script)
	if match == nil {
		t.Fatal("no key in the installer")
	}
	return script, string(match[1]), view
}

func decodeJSON(body io.Reader, into any) error { return json.NewDecoder(body).Decode(into) }

func storedKeyHash(t *testing.T, s *Store) string {
	t.Helper()
	var value string
	if err := s.read.QueryRow("SELECT value FROM settings WHERE key=?", photosKeySetting).Scan(&value); err != nil {
		return ""
	}
	return value
}

func TestPhotosKeyIsGeneratedStoredAsAHashAndKeptAcrossARestart(t *testing.T) {
	s := testStore(t)
	h, clock := setupHub(t, s, "")
	if h.Enabled() || h.Status().Configured {
		t.Fatal("a fresh server has a key")
	}
	_, token, _ := install(t, h)
	if len(token) != 43 || !validPhotosKey(token) {
		t.Fatalf("weak key of %d characters", len(token))
	}
	if !h.Enabled() || !h.authorised(token) || h.authorised(token+"x") {
		t.Fatal("the new key is not the one accepted")
	}
	sum := sha256.Sum256([]byte(token))
	if stored := storedKeyHash(t, s); stored != hex.EncodeToString(sum[:]) {
		t.Fatalf("stored %q", stored)
	}
	var dump strings.Builder
	rows, err := s.read.Query("SELECT key, value FROM settings")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k, v string
		rows.Scan(&k, &v)
		dump.WriteString(k + "=" + v + "\n")
	}
	rows.Close()
	if strings.Contains(dump.String(), token) {
		t.Fatal("the key itself was stored")
	}

	// A restart, or the other process, accepts the same key from the hash.
	again := NewPhotosHub(s, "")
	if !again.Enabled() || !again.authorised(token) {
		t.Fatal("the key did not survive a restart")
	}

	// Setting up again hands out a new key and stops accepting the old one.
	clock.advance(time.Minute)
	_, second, _ := install(t, h)
	if second == token || h.authorised(token) || !h.authorised(second) {
		t.Fatal("the key was not replaced")
	}
	if !NewPhotosHub(s, "").authorised(second) {
		t.Fatal("the replacement was not stored")
	}
}

func TestPhotosEnvironmentKeyWinsAndIsNeverReplaced(t *testing.T) {
	s := testStore(t)
	h, _ := setupHub(t, s, fakePhotosKey)
	_, token, _ := install(t, h)
	if token != fakePhotosKey || !h.authorised(fakePhotosKey) {
		t.Fatal("PHOTOS_AGENT_KEY was not the key handed out")
	}
	if storedKeyHash(t, s) != "" {
		t.Fatal("a generated key was stored beside PHOTOS_AGENT_KEY")
	}
	// A stored hash from an earlier setup is ignored while the variable is set.
	if err := s.savePhotosKeyHash(context.Background(), sha256.Sum256([]byte("an-older-generated-key-of-enough-length"))); err != nil {
		t.Fatal(err)
	}
	if h := NewPhotosHub(s, fakePhotosKey); !h.authorised(fakePhotosKey) || h.authorised("an-older-generated-key-of-enough-length") {
		t.Fatal("the stored key was accepted over PHOTOS_AGENT_KEY")
	}
}

func TestPhotosSetupCodeWorksOnce(t *testing.T) {
	h, clock := setupHub(t, testStore(t), "")
	view, err := h.NewSetup(fakeBase)
	if err != nil || view.State != "waiting" {
		t.Fatalf("new setup %+v %v", view, err)
	}
	code := codeOf(t, view)
	if len(code) < 22 {
		t.Fatalf("short code %q", code)
	}
	if strings.Contains(view.ID, code) || strings.Contains(code, view.ID) {
		t.Fatal("the id gives the code away")
	}
	script, err := h.Install(context.Background(), code, fakeBase)
	if err != nil {
		t.Fatal(err)
	}
	if next, _ := h.Setup(view.ID); next.State != "used" || next.Command != "" {
		t.Fatalf("after use %+v", next)
	}
	if _, err := h.Install(context.Background(), code, fakeBase); !errors.Is(err, photosInstallUnknown) {
		t.Fatalf("second use: %v", err)
	}
	if _, err := h.Install(context.Background(), "not-a-code", fakeBase); !errors.Is(err, photosInstallUnknown) {
		t.Fatalf("made-up code: %v", err)
	}

	// Connected once the new key is heard from, and not before.
	match := tokenLine.FindSubmatch(script)
	clock.advance(time.Second)
	if h.authorised("wrong-key-wrong-key-wrong-key-wrong-key") {
		t.Fatal("wrong key accepted")
	}
	if next, _ := h.Setup(view.ID); next.State != "used" {
		t.Fatalf("a refused request counted as connected: %+v", next)
	}
	if !h.authorised(string(match[1])) {
		t.Fatal("new key refused")
	}
	if next, _ := h.Setup(view.ID); next.State != "connected" {
		t.Fatalf("after the helper called in %+v", next)
	}
}

func TestPhotosSetupCodeExpires(t *testing.T) {
	h, clock := setupHub(t, testStore(t), "")
	view, _ := h.NewSetup(fakeBase)
	clock.advance(photosSetupLifetime)
	if next, _ := h.Setup(view.ID); next.State != "expired" {
		t.Fatalf("after 15 minutes %+v", next)
	}
	if _, err := h.Install(context.Background(), codeOf(t, view), fakeBase); !errors.Is(err, photosInstallUnknown) {
		t.Fatalf("expired code: %v", err)
	}
	if h.Enabled() {
		t.Fatal("an expired code handed out a key")
	}
	// Old codes are forgotten, and never more than a few are held.
	clock.advance(photosSetupLifetime)
	for range photosSetupMax + 3 {
		h.NewSetup(fakeBase)
	}
	if _, ok := h.Setup(view.ID); ok || len(h.setups) != photosSetupMax {
		t.Fatalf("held %d codes", len(h.setups))
	}
}

func TestPhotosSetupRefusesWhileApplyingAndKeepsTheCode(t *testing.T) {
	h, _ := setupHub(t, testStore(t), "")
	view, _ := h.NewSetup(fakeBase)
	h.job = &photosJob{id: "busy", state: "applying"}
	h.agent.at = h.now()
	if _, err := h.Install(context.Background(), codeOf(t, view), fakeBase); !errors.Is(err, photosInstallBusy) {
		t.Fatalf("while applying: %v", err)
	}
	h.job.state = "done"
	if _, err := h.Install(context.Background(), codeOf(t, view), fakeBase); err != nil {
		t.Fatalf("the refused attempt burned the code: %v", err)
	}
}

func TestPhotosSetupOnlyUsesPlainAddresses(t *testing.T) {
	h, _ := setupHub(t, testStore(t), "")
	for _, base := range []string{"http://cull'$(id)", "http://a b", "ftp://cull", "http://", "http://cull/path", "http://cull:8830\nx"} {
		if _, err := h.NewSetup(base); err == nil {
			t.Fatalf("accepted %q", base)
		}
	}
	for _, base := range []string{"http://192.168.1.10:8830", "https://cull.example.ts.net", "http://[fd7a:115c::1]:8830", "http://localhost"} {
		if !photosBaseOK(base) {
			t.Fatalf("refused %q", base)
		}
	}
	view, _ := h.NewSetup(fakeBase)
	if _, err := h.Install(context.Background(), codeOf(t, view), "http://evil;rm"); !errors.Is(err, photosInstallBadHost) {
		t.Fatalf("bad host: %v", err)
	}
	if _, err := h.Install(context.Background(), codeOf(t, view), fakeBase); err != nil {
		t.Fatalf("the bad host burned the code: %v", err)
	}
}

func TestPhotosInstallerCarriesTheKeyOnceAndTheSources(t *testing.T) {
	h, _ := setupHub(t, testStore(t), "")
	script, token, _ := install(t, h)
	text := string(script)
	if strings.Count(text, token) != 1 {
		t.Fatalf("the key appears %d times", strings.Count(text, token))
	}
	if !strings.HasPrefix(text, "#!/bin/bash\n") || strings.Count(text, "#!/bin/bash") != 1 {
		t.Fatal("the installer does not start with exactly one shebang")
	}
	if !strings.Contains(text, "CULL_SYNC_URL='"+fakeBase+"'\n") || !strings.HasSuffix(text, "main \"$@\"\n") {
		t.Fatal("address or entry point missing")
	}

	// The sources unpack to what build.sh needs, match the checksum, and are
	// the same bytes each time.
	block := regexp.MustCompile(`(?s)<<'CULL_SYNC_PAYLOAD'\n(.*?)\nCULL_SYNC_PAYLOAD\n`).FindStringSubmatch(text)
	if block == nil {
		t.Fatal("no payload")
	}
	payload, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(block[1], "\n", ""))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	if !strings.Contains(text, "CULL_SYNC_PAYLOAD_SHA256='"+hex.EncodeToString(sum[:])+"'\n") {
		t.Fatal("checksum does not match the payload")
	}
	again, err := packPhotosHelper(mac.CullSync)
	if err != nil || !bytes.Equal(again, payload) {
		t.Fatal("packing is not repeatable")
	}
	zipped, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]int64{}
	archive := tar.NewReader(zipped)
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names[header.Name] = header.Mode
	}
	for _, want := range []string{"CullSync/build.sh", "CullSync/Info.plist", "CullSync/Sources/App.swift", "CullSync/Sources/Agent.swift"} {
		if _, ok := names[want]; !ok {
			t.Fatalf("%s missing from %v", want, names)
		}
	}
	if names["CullSync/build.sh"] != 0o755 {
		t.Fatal("build.sh is not executable")
	}
	if _, ok := names["CullSync/setup.sh"]; ok {
		t.Fatal("the installer packed itself")
	}

	// The script parses, passes shellcheck where it is installed, and bash
	// unpacks the same payload. Only the definitions are run: main never is.
	file := filepath.Join(t.TempDir(), "setup.sh")
	if err := os.WriteFile(file, script, 0o600); err != nil {
		t.Fatal(err)
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash to check the installer with")
	}
	if out, err := exec.Command(bash, "-n", file).CombinedOutput(); err != nil {
		t.Fatalf("bash -n: %v\n%s", err, out)
	}
	if shellcheck, err := exec.LookPath("shellcheck"); err == nil {
		if out, err := exec.Command(shellcheck, "-s", "bash", file).CombinedOutput(); err != nil {
			t.Fatalf("shellcheck: %v\n%s", err, out)
		}
	}
	definitions := strings.TrimSuffix(text, "main \"$@\"\n") + "cull_sync_payload\n"
	unpacked, err := exec.Command(bash, "-c", definitions).Output()
	if err != nil {
		t.Fatalf("bash could not decode the payload: %v", err)
	}
	if !bytes.Equal(unpacked, payload) {
		t.Fatal("bash decoded different bytes")
	}
}

func TestPhotosInstallRoute(t *testing.T) {
	h, _ := setupHub(t, testStore(t), "")
	server := httptest.NewServer(h.Handler())
	t.Cleanup(server.Close)
	post := func(site string) *http.Response {
		req, _ := http.NewRequest("POST", server.URL+"/api/photos/setup", strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		if site != "" {
			req.Header.Set("Sec-Fetch-Site", site)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	if res := post("cross-site"); res.StatusCode != 403 {
		t.Fatalf("another site got a code: %d", res.StatusCode)
	}
	res := post("same-origin")
	var view PhotosSetupView
	if res.StatusCode != 200 || decodeJSON(res.Body, &view) != nil {
		t.Fatalf("setup: %d", res.StatusCode)
	}
	res.Body.Close()
	prefix := "curl -fsSL " + server.URL + "/api/photos/install/"
	if !strings.HasPrefix(view.Command, prefix) {
		t.Fatalf("command %q", view.Command)
	}
	url := strings.TrimSuffix(strings.TrimPrefix(view.Command, "curl -fsSL "), " | bash")
	fetch := func(method string) (string, *http.Response) {
		req, _ := http.NewRequest(method, url, nil)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		return string(body), res
	}

	// A HEAD, as a link preview might send, burns nothing.
	if body, res := fetch("HEAD"); body != "" || res.StatusCode != 200 {
		t.Fatalf("HEAD: %d %q", res.StatusCode, body)
	}
	first, res := fetch("GET")
	match := tokenLine.FindStringSubmatch(first)
	if match == nil || res.Header.Get("Cache-Control") != "no-store" || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/plain") {
		t.Fatalf("first fetch: %v %q", res.Header, first[:min(len(first), 200)])
	}
	second, _ := fetch("GET")
	if strings.Contains(second, match[1]) || tokenLine.MatchString(second) || !strings.Contains(second, "already been used") || !strings.Contains(second, "exit 1") {
		t.Fatalf("second fetch: %q", second)
	}
	var status PhotosSetupView
	res, _ = http.Get(server.URL + "/api/photos/setup/" + view.ID)
	if decodeJSON(res.Body, &status) != nil || status.State != "used" || status.Command != "" {
		t.Fatalf("status %+v", status)
	}
	res.Body.Close()

	// The helper gets in with the key it was given, and nothing else.
	agent := func(key string) int {
		req, _ := http.NewRequest("POST", server.URL+"/api/photos/agent/heartbeat", strings.NewReader(`{"version":"test","access":"authorized"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(PhotosAgentHeader, key)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if agent("guess-guess-guess-guess-guess-guess-guess") != 403 || agent(match[1]) != 200 {
		t.Fatal("the helper's key was not honoured")
	}
}
