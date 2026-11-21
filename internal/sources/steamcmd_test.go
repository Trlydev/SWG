package sources

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A fake steamcmd: a shell script that prints what the real one prints.
func fakeSteamCmd(t *testing.T, script string) string {
	if runtime.GOOS == "windows" {
		t.Skip("shell script stand-in")
	}
	p := filepath.Join(t.TempDir(), "steamcmd")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSteamCmdDownload(t *testing.T) {
	dest := t.TempDir()
	item := filepath.Join(dest, "steamapps", "workshop", "content", "1118200", "3838486260")
	ok := fakeSteamCmd(t, `
case "$*" in *"+force_install_dir "*"+login someone +workshop_download_item 1118200 3838486260 +quit"*) ;; *) echo "bad args: $*"; exit 1;; esac
mkdir -p "`+item+`"
echo 'Success. Downloaded item 3838486260 to "`+item+`" (554550 bytes)'
`)
	dir, err := SteamCmdDownload(context.Background(), ok, "someone", dest, 1118200, "3838486260")
	if err != nil || dir != item {
		t.Fatalf("download: %q %v", dir, err)
	}

	noLogin := fakeSteamCmd(t, `echo 'Cached credentials not found.'; echo 'FAILED (No cached credentials and @NoPromptForPassword is set)'; exit 5`)
	if _, err := SteamCmdDownload(context.Background(), noLogin, "someone", dest, 1118200, "3838486260"); !errors.Is(err, ErrSteamCmdLogin) {
		t.Errorf("not signed in: %v", err)
	}

	failed := fakeSteamCmd(t, `echo 'ERROR! Download item 3838486260 failed (Access Denied).'`)
	if _, err := SteamCmdDownload(context.Background(), failed, "someone", dest, 1118200, "3838486260"); err == nil || !strings.Contains(err.Error(), "Access Denied") {
		t.Errorf("failed download: %v", err)
	}

	elsewhere := fakeSteamCmd(t, `mkdir -p /tmp/ppgmods-steamcmd-elsewhere; echo 'Success. Downloaded item 1 to "/tmp/ppgmods-steamcmd-elsewhere" (1 bytes)'`)
	if _, err := SteamCmdDownload(context.Background(), elsewhere, "someone", dest, 1118200, "3838486260"); err == nil {
		t.Error("accepted a folder outside its own")
	}
	os.Remove("/tmp/ppgmods-steamcmd-elsewhere")

	for _, bad := range []string{"", "a b", "x;rm -rf /"} {
		if _, err := SteamCmdDownload(context.Background(), ok, bad, dest, 1118200, "3838486260"); err == nil {
			t.Errorf("account name %q accepted", bad)
		}
	}
	if _, err := SteamCmdDownload(context.Background(), "", "someone", dest, 1118200, "1"); !errors.Is(err, ErrNoSteamCmd) {
		t.Errorf("no steamcmd: %v", err)
	}
}
