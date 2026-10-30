// Package reppg installs and updates RE_PPG (github.com/AlibardaWasTaken/
// RE_PPG, MIT licence), the community loader that brings C# mods back to
// People Playground 1.27 and later, and the BepInEx it runs on.
//
// Nothing unverified is written to the game folder. RE_PPG signs its
// releases: base-manifest.json (inside RE_PPG-base.zip) and update.json list
// every file with its size and SHA-256, signed with the publisher's
// RSA-3072 key (PKCS#1 v1.5, SHA-256). ppgmods carries that public key
// itself, so a tampered download can't bring its own; it extracts only the
// files the signed list names, each checked against its hash. BepInEx comes
// from its official release, checked against a hash ppgmods carries.
package reppg

import (
	"archive/zip"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Trlydev/SWG/internal/game"
)

// RE_PPG's publisher key, from RE_PPG/bootstrap/publisher-public.xml.
const publisherModulus = "t3Z9WfUky2Atd8symj53JDbGtDZQFcULv5xECVoCG7a+wQoQmYLuiDOIsBGzr3gCZA+m1vy2+LvCJp8D7TwaPKi6OQgdh6Q7GbK4O6zN0TBvOyfebfHcsu+wPpV2VtUtrIC7XLe8H/GE+SOZ/uczAl06PheFv1XC42E471oR6kT6IcQuETnFLmJIIj1RBG1Pli8darfZYtpP2WgWUbvPBmnBYl29EcHaPRq/WD63TuSOIb/DhvfN8YV0T6n91HAwtvzhXzoisS6D7UiG37PsWtCfsZ3oS/1B9A1FJ++0npN3maw94mUCOyVWoKf3xshsoHHOcxLcO0dBb5zLZ715BKSS1jXTiZG0bSBLngZF1cbroga5fptxykZmI0Q4jD28udDTOsBKLxgZ3KgJhGEP2lss11VJN2d7T6M9yrZI1QzhyePatGnuK16jO8N65/wzbqe0zji6YGbdC59jIvaWc3E5W47TN0mnvpYwKtby6WxBjHbYi7OjPo4QaShJZMCN"

var (
	publisherKey = func() *rsa.PublicKey {
		n, _ := base64.StdEncoding.DecodeString(publisherModulus)
		return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: 65537}
	}()

	releases = "https://github.com/AlibardaWasTaken/RE_PPG/releases"

	// BepInEx 5.4.23.5 for Windows x64, the version RE_PPG asks for. The
	// hash matches ppgmods' own index of the official release files.
	bepInExURL    = "https://github.com/BepInEx/BepInEx/releases/download/v5.4.23.5/BepInEx_win_x64_5.4.23.5.zip"
	bepInExSHA256 = "82f9878551030f54657792c0740d9d51a09500eeae1fba21106b0c441e6732c4"

	client = &http.Client{Timeout: 10 * time.Minute}
)

const maxDownload = 300 << 20

// Manifest is a signed RE_PPG file list (update.json or base-manifest.json).
type Manifest struct {
	Product  string `json:"Product"`
	Version  string `json:"Version"`
	Sequence int    `json:"Sequence"`
	Files    []File `json:"Files"`
}

// File is one file a manifest vouches for.
type File struct {
	Path   string `json:"Path"`
	Size   int64  `json:"Size"`
	Sha256 string `json:"Sha256"`
}

// ParseSigned checks a signed manifest's signature and returns its content.
func ParseSigned(b []byte) (*Manifest, error) {
	var env struct{ Payload, Signature string }
	if err := json.Unmarshal(b, &env); err != nil {
		return nil, fmt.Errorf("not a signed RE_PPG manifest: %w", err)
	}
	payload, err := base64.StdEncoding.DecodeString(env.Payload)
	if err != nil {
		return nil, errors.New("signed RE_PPG manifest: bad payload")
	}
	sig, err := base64.StdEncoding.DecodeString(env.Signature)
	if err != nil {
		return nil, errors.New("signed RE_PPG manifest: bad signature")
	}
	h := sha256.Sum256(payload)
	if rsa.VerifyPKCS1v15(publisherKey, crypto.SHA256, h[:], sig) != nil {
		return nil, errors.New("RE_PPG manifest is not signed by RE_PPG's publisher key")
	}
	var m Manifest
	if err := json.Unmarshal(payload, &m); err != nil {
		return nil, fmt.Errorf("signed RE_PPG manifest: %w", err)
	}
	return &m, nil
}

// Installed is the version of the RE_PPG base files in the game folder ("" if
// none or unverifiable). RE_PPG may have updated its runtime in-game since.
func Installed(gameDir string) string {
	b, err := os.ReadFile(filepath.Join(gameDir, "RE_PPG", "bootstrap", "seed", "update.json"))
	if err != nil {
		return ""
	}
	m, err := ParseSigned(b)
	if err != nil {
		return ""
	}
	return m.Version
}

// Latest is the newest RE_PPG release, from its signed update.json.
func Latest(ctx context.Context) (*Manifest, error) {
	b, err := get(ctx, releases+"/latest/download/update.json", 1<<20)
	if err != nil {
		return nil, fmt.Errorf("checking RE_PPG's latest release: %w", err)
	}
	return ParseSigned(b)
}

// Result is what Install did.
type Result struct {
	Version string // RE_PPG version installed
	// Proton: on Linux (the game running in Proton), whether Wine was told
	// to load BepInEx: ProtonSet, ProtonAlready or ProtonNoPrefix; "" elsewhere.
	Proton string
}

// Install installs or updates RE_PPG (and BepInEx when it is missing) in
// gameDir. On Linux it also tells the game's Proton prefix to load BepInEx.
func Install(ctx context.Context, gameDir string, logf func(string, ...any)) (Result, error) {
	v, err := install(ctx, gameDir, logf)
	if err != nil {
		return Result{}, err
	}
	res := Result{Version: v}
	if runtime.GOOS != "windows" {
		if logf == nil {
			logf = func(string, ...any) {}
		}
		res.Proton, err = setWinhttpOverride(ProtonPrefix(gameDir))
		switch {
		case err != nil:
			return res, fmt.Errorf("RE_PPG is installed, but ppgmods couldn't tell Proton to load BepInEx (%v): add this to the game's Steam launch options: %s", err, LaunchOption)
		case res.Proton == ProtonSet:
			logf("Proton: told Wine to load BepInEx from the game folder (winhttp DLL override in the game's prefix)")
		case res.Proton == ProtonNoPrefix:
			logf("Proton: the game hasn't run through Proton yet. Start it once from Steam, close it, and install RE_PPG again (or add this to its launch options: %s)", LaunchOption)
		}
	}
	return res, nil
}

func install(ctx context.Context, gameDir string, logf func(string, ...any)) (string, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if gameDir == "" {
		return "", errors.New("the game folder is not set")
	}
	if game.DetectLoader(gameDir).NativeLinux {
		return "", errors.New("People Playground runs as its native Linux version here, and RE_PPG only works in the Windows version: in Steam, People Playground, Properties, Compatibility, force Proton, let Steam install the Windows version, then install RE_PPG")
	}
	if _, err := os.Stat(filepath.Join(gameDir, "BepInEx", "core", "BepInEx.dll")); err != nil {
		logf("downloading BepInEx 5.4.23.5 (the loader RE_PPG runs on)")
		if err := installBepInEx(ctx, gameDir); err != nil {
			return "", fmt.Errorf("installing BepInEx: %w", err)
		}
		logf("installed BepInEx 5.4.23.5")
	}
	latest, err := Latest(ctx)
	if err != nil {
		return "", err
	}
	if !safeVersion(latest.Version) {
		return "", fmt.Errorf("RE_PPG's latest release has an unexpected version %q", latest.Version)
	}
	logf("downloading RE_PPG %s", latest.Version)
	zf, err := download(ctx, releases+"/download/v"+latest.Version+"/RE_PPG-base.zip")
	if err != nil {
		return "", fmt.Errorf("downloading RE_PPG: %w", err)
	}
	defer os.Remove(zf)
	zr, err := zip.OpenReader(zf)
	if err != nil {
		return "", fmt.Errorf("RE_PPG-base.zip: %w", err)
	}
	defer zr.Close()
	entries := map[string]*zip.File{}
	for _, f := range zr.File {
		entries[zipPath(f.Name)] = f
	}
	mf := entries["base-manifest.json"]
	if mf == nil {
		return "", errors.New("RE_PPG-base.zip has no base-manifest.json")
	}
	raw, err := readEntry(mf, 4<<20)
	if err != nil {
		return "", err
	}
	man, err := ParseSigned(raw)
	if err != nil {
		return "", err
	}
	if man.Product != "RE_PPG-base" || len(man.Files) == 0 {
		return "", fmt.Errorf("RE_PPG-base.zip: unexpected manifest %q", man.Product)
	}
	// Check every file before writing any.
	type item struct {
		f   File
		ent *zip.File
	}
	var items []item
	for _, f := range man.Files {
		p := zipPath(f.Path)
		if !allowed(p, "BepInEx/", "RE_PPG/") {
			return "", fmt.Errorf("RE_PPG's manifest names a file outside BepInEx/ and RE_PPG/: %q", f.Path)
		}
		ent := entries[p]
		if ent == nil {
			return "", fmt.Errorf("RE_PPG-base.zip is missing %s", p)
		}
		if err := checkEntry(ent, f.Size, f.Sha256); err != nil {
			return "", fmt.Errorf("RE_PPG-base.zip: %s: %w", p, err)
		}
		items = append(items, item{File{p, f.Size, f.Sha256}, ent})
	}
	for _, it := range items {
		if err := writeEntry(it.ent, gameDir, it.f.Path, it.f.Size); err != nil {
			return "", fmt.Errorf("writing %s: %w (is the game running?)", it.f.Path, err)
		}
	}
	logf("installed RE_PPG %s (%d files, each checked against RE_PPG's signed list)", latest.Version, len(items))
	return latest.Version, nil
}

// installBepInEx extracts the official BepInEx release into gameDir.
func installBepInEx(ctx context.Context, gameDir string) error {
	zf, err := download(ctx, bepInExURL)
	if err != nil {
		return err
	}
	defer os.Remove(zf)
	if h, err := fileSHA(zf); err != nil || h != bepInExSHA256 {
		return errors.New("the BepInEx download does not match its official release")
	}
	zr, err := zip.OpenReader(zf)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		p := zipPath(f.Name)
		if f.FileInfo().IsDir() || p == "" {
			continue
		}
		if !allowed(p, "BepInEx/") && strings.Contains(p, "/") {
			return fmt.Errorf("unexpected path in BepInEx's zip: %q", f.Name)
		}
		if err := writeEntry(f, gameDir, p, int64(f.UncompressedSize64)); err != nil {
			return err
		}
	}
	return nil
}

// zipPath normalises a zip entry name (RE_PPG's use backslashes).
func zipPath(n string) string {
	return strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(n, `\`, "/")), "/")
}

func allowed(p string, prefixes ...string) bool {
	if p == "" || p == "." || strings.HasPrefix(p, "../") || strings.Contains(p, ":") {
		return false
	}
	for _, pre := range prefixes {
		if strings.HasPrefix(p, pre) {
			return true
		}
	}
	return false
}

// Newer reports whether version a is newer than b ("" is older than any).
func Newer(a, b string) bool {
	if b == "" {
		return a != ""
	}
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			fmt.Sscan(pa[i], &x)
		}
		if i < len(pb) {
			fmt.Sscan(pb[i], &y)
		}
		if x != y {
			return x > y
		}
	}
	return false
}

func safeVersion(v string) bool {
	if v == "" || len(v) > 32 {
		return false
	}
	for _, c := range v {
		if !(c >= '0' && c <= '9' || c == '.') {
			return false
		}
	}
	return true
}

func readEntry(f *zip.File, max int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, max))
}

func checkEntry(f *zip.File, size int64, sum string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(rc, size+1))
	if err != nil {
		return err
	}
	if n != size || !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), sum) {
		return errors.New("does not match RE_PPG's signed list")
	}
	return nil
}

// writeEntry writes a zip entry to dir/p through a temporary file.
func writeEntry(f *zip.File, dir, p string, size int64) error {
	dst := filepath.Join(dir, filepath.FromSlash(p))
	if rel, err := filepath.Rel(dir, dst); err != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("path %q leaves the game folder", p)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".ppgmods-*")
	if err != nil {
		return err
	}
	_, err = io.Copy(tmp, io.LimitReader(rc, size))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), dst)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

func get(ctx context.Context, u string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: %s", u, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, max))
}

// download saves u to a temporary file and returns its path.
func download(ctx context.Context, u string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("%s: %s", u, resp.Status)
	}
	f, err := os.CreateTemp("", "ppgmods-reppg-*.zip")
	if err != nil {
		return "", err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, maxDownload+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > maxDownload {
		err = errors.New("download too large")
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func fileSHA(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
