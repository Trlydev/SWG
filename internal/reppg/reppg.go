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
	"bytes"
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
	"strings"
	"time"

	"github.com/Trlydev/SWG/internal/game"
	"github.com/Trlydev/SWG/internal/scan"
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
	// GameAssemblySha256 is the SHA-256 of the game's Assembly-CSharp.dll
	// the release was made for (update.json only). RE_PPG's own installer
	// refuses another game version unless told --ignore-game-hash.
	GameAssemblySha256 string `json:"GameAssemblySha256,omitempty"`
	Files              []File `json:"Files"`
}

// ErrGameVersion: the game isn't the version the RE_PPG release was made
// for (People Playground updated, or RE_PPG did first). InstallAnyGame
// installs anyway, like RE_PPG's installer with --ignore-game-hash.
var ErrGameVersion = errors.New("this People Playground version isn't the one RE_PPG was made for")

// GameAssembly is the game file RE_PPG's releases name by hash.
func GameAssembly(gameDir string) string {
	return filepath.Join(gameDir, "People Playground_Data", "Managed", "Assembly-CSharp.dll")
}

// GameMatches reports whether gameDir holds the game version m was made for.
// known is false when m names no version or the game file can't be read.
func GameMatches(gameDir string, m *Manifest) (ok, known bool) {
	if m == nil || m.GameAssemblySha256 == "" {
		return false, false
	}
	h, err := fileSHA(GameAssembly(gameDir))
	if err != nil {
		return false, false
	}
	return strings.EqualFold(h, m.GameAssemblySha256), true
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
	if v := activeRelease(gameDir); v != "" {
		return v
	}
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

// activeRelease is the release RE_PPG's own updater runs: it keeps each
// release it installed in RE_PPG/versions/<release id> (ids like v0.2.36)
// and records the active one in a state.json, so after it updated itself
// the seed update.json ppgmods installed names an older version. The
// state is read loosely (its exact layout isn't published): a value under
// an "active"/"current"/"installed" key that names a release folder wins;
// otherwise the newest release folder.
func activeRelease(gameDir string) string {
	root := filepath.Join(gameDir, "RE_PPG")
	ents, err := os.ReadDir(filepath.Join(root, "versions"))
	if err != nil {
		return ""
	}
	have := map[string]string{} // release id -> version
	newest := ""
	for _, e := range ents {
		v := strings.TrimPrefix(strings.TrimPrefix(e.Name(), "v"), "V")
		if !e.IsDir() || !safeVersion(v) {
			continue
		}
		have[strings.ToLower(e.Name())] = v
		if newest == "" || Newer(v, newest) {
			newest = v
		}
	}
	for _, p := range []string{filepath.Join(root, "state.json"), filepath.Join(root, "versions", "state.json"), filepath.Join(root, "updater", "state.json")} {
		b, err := os.ReadFile(p)
		if err != nil || len(b) > 1<<20 {
			continue
		}
		var st any
		if json.Unmarshal(b, &st) != nil {
			continue
		}
		if v := activeIn(st, have, 0); v != "" {
			return v
		}
	}
	return newest
}

// activeIn looks for a release id under an active/current/installed key.
func activeIn(x any, have map[string]string, depth int) string {
	if depth > 6 {
		return ""
	}
	switch t := x.(type) {
	case map[string]any:
		for k, v := range t {
			lk := strings.ToLower(k)
			if s, ok := v.(string); ok && (strings.Contains(lk, "active") || strings.Contains(lk, "current") || strings.Contains(lk, "installed")) {
				if ver, ok := have[strings.ToLower(s)]; ok {
					return ver
				}
			}
		}
		for _, v := range t {
			if r := activeIn(v, have, depth+1); r != "" {
				return r
			}
		}
	case []any:
		for _, v := range t {
			if r := activeIn(v, have, depth+1); r != "" {
				return r
			}
		}
	}
	return ""
}

// Latest is the newest RE_PPG release, from its signed update.json.
func Latest(ctx context.Context) (*Manifest, error) {
	b, err := get(ctx, releases+"/latest/download/update.json", 1<<20)
	if err != nil {
		return nil, fmt.Errorf("checking RE_PPG's latest release: %w", err)
	}
	return ParseSigned(b)
}

// Install installs or updates RE_PPG (and BepInEx when it is missing) in
// gameDir and returns the version installed. It refuses (ErrGameVersion)
// when the game isn't the version the release was made for.
func Install(ctx context.Context, gameDir string, logf func(string, ...any)) (string, error) {
	return install(ctx, gameDir, false, logf)
}

// InstallAnyGame is Install without the game version check.
func InstallAnyGame(ctx context.Context, gameDir string, logf func(string, ...any)) (string, error) {
	return install(ctx, gameDir, true, logf)
}

func install(ctx context.Context, gameDir string, anyGame bool, logf func(string, ...any)) (string, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if gameDir == "" {
		return "", errors.New("the game folder is not set")
	}
	if game.DetectLoader(gameDir).NativeLinux {
		return "", errors.New("People Playground runs as its native Linux version here, and RE_PPG only works in the Windows version: in Steam, People Playground, Properties, Compatibility, force Proton, let Steam install the Windows version, then install RE_PPG")
	}
	latest, err := Latest(ctx)
	if err != nil {
		return "", err
	}
	if !safeVersion(latest.Version) {
		return "", fmt.Errorf("RE_PPG's latest release has an unexpected version %q", latest.Version)
	}
	// The game version, before anything is written (BepInEx included).
	if latest.GameAssemblySha256 != "" {
		if _, err := os.Stat(GameAssembly(gameDir)); err != nil {
			return "", fmt.Errorf("can't find the game's People Playground_Data/Managed/Assembly-CSharp.dll: is this the People Playground folder? (%w)", err)
		}
		switch ok, _ := GameMatches(gameDir, latest); {
		case ok:
		case anyGame:
			logf("installing RE_PPG %s although it was made for another People Playground version (you chose to)", latest.Version)
		default:
			return "", fmt.Errorf("%w: RE_PPG %s was made for another People Playground version (did the game update?). RE_PPG's own installer stops here too; wait for an RE_PPG update, or install anyway and it may not load", ErrGameVersion, latest.Version)
		}
	}
	if missing := bepInExMissing(gameDir); missing != "" {
		logf("downloading BepInEx 5.4.23.5 (the loader RE_PPG runs on; %s is missing)", missing)
		if err := installBepInEx(ctx, gameDir); err != nil {
			return "", fmt.Errorf("installing BepInEx: %w", err)
		}
		logf("installed BepInEx 5.4.23.5")
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
	verified := func(p string) []byte {
		for _, it := range items {
			if it.f.Path == p {
				b, err := readEntry(it.ent, it.f.Size)
				if err == nil && int64(len(b)) == it.f.Size {
					return b
				}
			}
		}
		return nil
	}
	// The base manifest carries no version; the package's own signed
	// seed/update.json does, and must be the latest release.
	if seed := verified("RE_PPG/bootstrap/seed/update.json"); seed != nil {
		sm, err := ParseSigned(seed)
		if err != nil {
			return "", fmt.Errorf("RE_PPG-base.zip: its update.json: %w", err)
		}
		if sm.Version != latest.Version {
			return "", fmt.Errorf("RE_PPG-base.zip is version %q, not the latest release %s", sm.Version, latest.Version)
		}
	}
	plan, err := planHarmony(gameDir, verified)
	if err != nil {
		return "", err
	}
	for _, it := range items {
		if err := writeEntry(it.ent, gameDir, it.f.Path, it.f.Size); err != nil {
			return "", fmt.Errorf("writing %s: %w (is the game running?)", it.f.Path, err)
		}
	}
	if err := plan.apply(gameDir, logf); err != nil {
		return "", err
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

// legacyHarmonySHA256 is the Harmony RE_PPG puts in the game's Managed
// folder for older mods (RE_PPG's installer pins the same hash).
var legacyHarmonySHA256 = "0de0118c8f1d4408de389ca33b46d2ff7778f3a8541b430cae729ec913d899c7"

// bepInExFiles must all be present for BepInEx to count as installed: a
// half-finished extraction is repaired by installing it again.
var bepInExFiles = []string{"winhttp.dll", "doorstop_config.ini", "BepInEx/core/BepInEx.dll", "BepInEx/core/BepInEx.Preloader.dll",
	"BepInEx/core/0Harmony.dll", "BepInEx/core/MonoMod.Utils.dll", "BepInEx/core/MonoMod.RuntimeDetour.dll", "BepInEx/core/Mono.Cecil.dll"}

// bepInExMissing names a BepInEx file the game folder lacks ("" if none).
func bepInExMissing(gameDir string) string {
	for _, f := range bepInExFiles {
		if _, err := os.Stat(filepath.Join(gameDir, filepath.FromSlash(f))); err != nil {
			return f
		}
	}
	return ""
}

// harmonyPlan is what RE_PPG's installer does after copying its files,
// decided before anything is written:
//   - BepInEx's Harmony is replaced by RE_PPG's patched one, when BepInEx's
//     is the version RE_PPG patched (compatibility.json: its hash, and the
//     hashes of the libraries next to it);
//   - older mods get a Harmony in People Playground_Data/Managed, unless the
//     game already has one there.
//
// The files come from the signed, checked package (never from the folder).
type harmonyPlan struct {
	core, managed []byte // what to write; nil: nothing
}

const (
	compatPath  = "RE_PPG/bootstrap/harmony/compatibility.json"
	patchedPath = "RE_PPG/bootstrap/harmony/0Harmony.dll"
	legacyPath  = "RE_PPG/bootstrap/legacy-harmony/0Harmony.dll"
)

// planHarmony checks the game folder against RE_PPG's Harmony files.
// verified returns a file of the signed package (nil if it isn't in it).
func planHarmony(gameDir string, verified func(string) []byte) (*harmonyPlan, error) {
	plan := &harmonyPlan{}
	raw, patched := verified(compatPath), verified(patchedPath)
	if raw == nil && patched == nil {
		return plan, nil // a release without the Harmony step
	}
	if raw == nil || patched == nil {
		return nil, errors.New("RE_PPG's package lists only part of its Harmony files")
	}
	var compat struct {
		OriginalSha256, PatchedSha256 string
		Dependencies                  map[string]string
	}
	if err := json.Unmarshal(bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf")), &compat); err != nil || compat.OriginalSha256 == "" || compat.PatchedSha256 == "" {
		return nil, errors.New("RE_PPG's Harmony compatibility list is invalid")
	}
	if !strings.EqualFold(sha(patched), compat.PatchedSha256) {
		return nil, errors.New("RE_PPG's patched Harmony doesn't match its compatibility list")
	}
	core := filepath.Join(gameDir, "BepInEx", "core")
	cur, err := fileSHA(filepath.Join(core, "0Harmony.dll"))
	switch {
	case err != nil:
		return nil, errors.New("BepInEx/core/0Harmony.dll is missing: reinstall BepInEx 5.4.23.5 (delete the BepInEx folder and install RE_PPG again)")
	case strings.EqualFold(cur, compat.PatchedSha256):
		// already RE_PPG's
	case strings.EqualFold(cur, compat.OriginalSha256):
		for name, want := range compat.Dependencies {
			if h, err := fileSHA(filepath.Join(core, filepath.Base(name))); err != nil || !strings.EqualFold(h, want) {
				return nil, fmt.Errorf("BepInEx/core/%s isn't from BepInEx 5.4.23.5, which RE_PPG needs: delete the BepInEx folder and install RE_PPG again", filepath.Base(name))
			}
		}
		plan.core = patched
	default:
		return nil, errors.New("BepInEx/core/0Harmony.dll isn't from BepInEx 5.4.23.5, which RE_PPG needs: delete the BepInEx folder and install RE_PPG again")
	}

	legacy := verified(legacyPath)
	if legacy == nil || sha(legacy) != legacyHarmonySHA256 {
		return nil, errors.New("RE_PPG's Harmony for older mods is missing from its package or doesn't match the expected file")
	}
	dst := filepath.Join(gameDir, "People Playground_Data", "Managed", "0Harmony.dll")
	if b, err := os.ReadFile(dst); err == nil {
		if sha(b) != legacyHarmonySHA256 {
			if info, err := scan.ReadCLR(b); err != nil || info.Assembly != "0Harmony" {
				return nil, errors.New("People Playground_Data/Managed/0Harmony.dll isn't Harmony, so older mods can't use it; RE_PPG's installer stops here too")
			}
		}
		return plan, nil // the game's own, or RE_PPG's from an earlier install
	}
	plan.managed = legacy
	return plan, nil
}

func (p *harmonyPlan) apply(gameDir string, logf func(string, ...any)) error {
	if p.core != nil {
		if err := writeFile(gameDir, "BepInEx/core/0Harmony.dll", p.core); err != nil {
			return fmt.Errorf("installing RE_PPG's Harmony: %w (is the game running?)", err)
		}
		logf("replaced BepInEx's Harmony with RE_PPG's patched one")
	}
	if p.managed != nil {
		if err := writeFile(gameDir, "People Playground_Data/Managed/0Harmony.dll", p.managed); err != nil {
			return fmt.Errorf("installing Harmony for older mods: %w (is the game running?)", err)
		}
		logf("installed Harmony for older mods in People Playground_Data/Managed")
	}
	return nil
}

func sha(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// writeFile writes b to dir/p through a temporary file.
func writeFile(dir, p string, b []byte) error {
	return writeFrom(dir, p, bytes.NewReader(b), int64(len(b)))
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
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	return writeFrom(dir, p, rc, size)
}

// writeFrom writes up to size bytes of r to dir/p (readable by all, like the
// game's own files). The file must stay inside dir, also when a folder on
// the way is a symbolic link.
func writeFrom(dir, p string, r io.Reader, size int64) error {
	dst := filepath.Join(dir, filepath.FromSlash(p))
	if !inside(dir, dst) {
		return fmt.Errorf("path %q leaves the game folder", p)
	}
	// Check where the folders lead before creating any: the deepest one that
	// exists, resolved, must be in the game folder (and again once made).
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	staysIn := func() bool {
		at := filepath.Dir(dst)
		for {
			if _, err := os.Lstat(at); err == nil {
				break
			}
			up := filepath.Dir(at)
			if up == at {
				return false
			}
			at = up
		}
		real, err := filepath.EvalSymlinks(at)
		return err == nil && (real == realDir || inside(realDir, real))
	}
	if !staysIn() {
		return fmt.Errorf("path %q leads out of the game folder (a symbolic link)", p)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if !staysIn() {
		return fmt.Errorf("path %q leads out of the game folder (a symbolic link)", p)
	}
	if fi, err := os.Lstat(dst); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%q is a symbolic link; not replacing it", p)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".ppgmods-*")
	if err != nil {
		return err
	}
	_, err = io.Copy(tmp, io.LimitReader(r, size))
	if err == nil {
		err = tmp.Chmod(0o644)
	}
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

// inside reports whether path is below dir.
func inside(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// fetch GETs u, once more after a moment if GitHub answers with a server
// error (they come and go).
func fetch(ctx context.Context, u string) (*http.Response, error) {
	for try := 0; ; try++ {
		req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil || resp.StatusCode < 500 || try == 1 {
			return resp, err
		}
		resp.Body.Close()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

func get(ctx context.Context, u string, max int64) ([]byte, error) {
	resp, err := fetch(ctx, u)
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
	resp, err := fetch(ctx, u)
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
