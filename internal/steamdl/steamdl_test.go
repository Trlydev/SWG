package steamdl

import (
	"archive/zip"
	"bufio"
	"bytes"
	"compress/flate"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The QR code DepotDownloader 3.4.0 printed (captured from a real run) reads
// back as a square grid of modules.
func TestQRFromRealOutput(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "qr-output.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var rows []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if r, ok := qrRow(sc.Text()); ok {
			rows = append(rows, r)
		}
	}
	rows = trimQR(rows)
	if len(rows) < 21 || len(rows) != len(rows[0]) {
		t.Fatalf("QR grid %d rows of %d", len(rows), len(rows[0]))
	}
	// A QR code's top-left finder pattern: a 7-module dark ring.
	if !strings.HasPrefix(rows[0], "1111111") || !strings.HasPrefix(rows[6], "1111111") || rows[3][:7] != "1011101" {
		t.Errorf("no finder pattern:\n%s", strings.Join(rows[:7], "\n"))
	}
	if _, ok := qrRow("Connecting to Steam3... Done!"); ok {
		t.Error("text read as a QR row")
	}
}

func protoString(field int, s string) []byte {
	b := []byte{byte(field<<3 | 2)}
	b = append(b, byte(len(s)))
	return append(b, s...)
}

// writeStore writes a DepotDownloader account.config holding a session for
// each account.
func writeStore(t *testing.T, path string, accounts ...string) {
	var msg []byte
	msg = append(msg, protoString(2, "x")...) // another field, skipped
	for _, a := range accounts {
		entry := append(protoString(1, a), protoString(2, "eyJ.token")...)
		msg = append(msg, 4<<3|2, byte(len(entry)))
		msg = append(msg, entry...)
	}
	var buf bytes.Buffer
	w, _ := flate.NewWriter(&buf, flate.DefaultCompression)
	w.Write(msg)
	w.Close()
	os.MkdirAll(filepath.Dir(path), 0o700)
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func storePath(tl *Tool) string {
	return filepath.Join(tl.home(), ".local", "share", "IsolatedStorage", "abc", "url.x", "AssemFiles", "account.config")
}

func TestAccount(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the store lives elsewhere on Windows")
	}
	tl := &Tool{Dir: t.TempDir()}
	if tl.Account() != "" {
		t.Fatal("account without a session")
	}
	writeStore(t, storePath(tl), "someone")
	if got := tl.Account(); got != "someone" {
		t.Errorf("one saved session: %q", got)
	}
	writeStore(t, storePath(tl), "someone", "other")
	os.WriteFile(filepath.Join(tl.Dir, "account"), []byte("other"), 0o600)
	if got := tl.Account(); got != "other" {
		t.Errorf("ppgmods' note: %q", got)
	}
	tl.SignOut()
	if tl.Account() != "" {
		t.Error("still signed in after signing out")
	}
}

// Install takes only the release whose SHA-256 ppgmods carries.
func TestInstallChecksTheRelease(t *testing.T) {
	a, ok := assets[runtime.GOOS+"/"+runtime.GOARCH]
	if !ok {
		t.Skip("no release for this computer")
	}
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	w, _ := zw.Create(filepath.Base((&Tool{}).exe()))
	w.Write([]byte("#!/bin/sh\n"))
	w, _ = zw.Create("LICENSE")
	w.Write([]byte("GPL"))
	w, _ = zw.Create("../escape")
	w.Write([]byte("x"))
	zw.Close()
	serve := func(b []byte) func(string, string, int64) (string, error) {
		return func(u, p string, _ int64) (string, error) {
			if !strings.HasSuffix(u, "/DepotDownloader_"+Version+"/"+a.name) {
				t.Errorf("fetched %s", u)
			}
			os.WriteFile(p, b, 0o644)
			h := sha256.Sum256(b)
			return hex.EncodeToString(h[:]), nil
		}
	}
	tl := &Tool{Dir: t.TempDir(), Download: serve(zb.Bytes())}
	if err := tl.Install(); err == nil || tl.Installed() {
		t.Fatal("installed a file that isn't the release ppgmods knows")
	}
	old := assets[runtime.GOOS+"/"+runtime.GOARCH]
	h := sha256.Sum256(zb.Bytes())
	assets[runtime.GOOS+"/"+runtime.GOARCH] = struct{ name, sha256 string }{a.name, hex.EncodeToString(h[:])}
	defer func() { assets[runtime.GOOS+"/"+runtime.GOARCH] = old }()
	if err := tl.Install(); err != nil || !tl.Installed() {
		t.Fatalf("matching release: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tl.Dir, "escape")); err == nil {
		t.Error("an entry outside the release's files was written")
	}
}

// fakeTool is a Tool whose DepotDownloader is a shell script.
func fakeTool(t *testing.T, script string) *Tool {
	if runtime.GOOS == "windows" {
		t.Skip("shell script stand-in")
	}
	tl := &Tool{Dir: t.TempDir()}
	os.MkdirAll(filepath.Dir(tl.exe()), 0o700)
	if err := os.WriteFile(tl.exe(), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return tl
}

func TestSignInAndDownload(t *testing.T) {
	qr, _ := os.ReadFile(filepath.Join("testdata", "qr-output.txt"))
	os.WriteFile(filepath.Join(t.TempDir(), "x"), nil, 0o644)
	tl := fakeTool(t, "")
	// The stand-in prints the real QR output, then saves a session the
	// way DepotDownloader does.
	store := storePath(tl)
	writeStore(t, store+".ready", "someone")
	script := "cat '" + filepath.Join(mustAbs(t, "testdata"), "qr-output.txt") + "'\n" +
		"case \"$*\" in *-qr*-remember-password*-loginid*) cp '" + store + ".ready' '" + store + "' ;; esac\n"
	os.WriteFile(tl.exe(), []byte("#!/bin/sh\n"+script), 0o755)
	_ = qr
	var shown [][]string
	acct, err := tl.SignIn(context.Background(), func(rows []string) { shown = append(shown, rows) })
	if err != nil || acct != "someone" || len(shown) != 1 {
		t.Fatalf("sign in: %q %v (%d codes shown)", acct, err, len(shown))
	}

	// Downloading with the saved session.
	os.WriteFile(tl.exe(), []byte(`#!/bin/sh
case "$*" in *"-pubfile 3838486260 -username someone -remember-password -loginid "*) ;; *) echo "bad args $*"; exit 1;; esac
dir=""; while [ $# -gt 0 ]; do [ "$1" = "-dir" ] && dir="$2"; shift; done
mkdir -p "$dir/.DepotDownloader"; echo scene > "$dir/3838486260.jaap"; echo "Total downloaded: 1 bytes"
`), 0o755)
	dest := filepath.Join(tl.Dir, "items", "3838486260")
	dir, err := tl.DownloadItem(context.Background(), "3838486260", dest)
	if err != nil || dir != dest {
		t.Fatalf("download: %q %v", dir, err)
	}
	if _, err := os.Stat(filepath.Join(dest, ".DepotDownloader")); err == nil {
		t.Error("DepotDownloader's bookkeeping left in the item")
	}

	// Steam ended the session: it asks for a password, which ppgmods never
	// answers.
	os.WriteFile(tl.exe(), []byte("#!/bin/sh\nprintf 'Enter account password for \"someone\": '\nsleep 30\n"), 0o755)
	if _, err := tl.DownloadItem(context.Background(), "3838486260", dest); !errors.Is(err, ErrSignIn) {
		t.Errorf("session ended: %v", err)
	}
	tl.SignOut()
	if _, err := tl.DownloadItem(context.Background(), "3838486260", dest); !errors.Is(err, ErrSignIn) {
		t.Errorf("signed out: %v", err)
	}
}

func mustAbs(t *testing.T, p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
