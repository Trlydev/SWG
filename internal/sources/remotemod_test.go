package sources

import (
	"archive/zip"
	"bytes"
	"errors"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// mod.json is read from a zip by fetching its table of contents and its own
// bytes, not the whole archive.
func TestRemoteModJSON(t *testing.T) {
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	big, _ := zw.CreateHeader(&zip.FileHeader{Name: "Mod/Sprites/big.png", Method: zip.Store})
	noise := make([]byte, 3<<20) // incompressible, so the archive is big
	rand.New(rand.NewSource(1)).Read(noise)
	big.Write(noise)
	w, _ := zw.Create("Mod/Deeper/mod.json")
	w.Write([]byte(`{"Name":"deeper"}`))
	w, _ = zw.Create("Mod/mod.json")
	w.Write([]byte(`{"Name":"Chemical Warfare","CreatorUGCIdentity":"2226697361"}`))
	zw.Close()
	data := zb.Bytes()

	var sent atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		cw := &countingWriter{ResponseWriter: rw, n: &sent}
		if r.URL.Path == "/noranges.zip" {
			rw.Write(data[:1024])
			return
		}
		http.ServeContent(cw, r, "mod.zip", time.Time{}, bytes.NewReader(data))
	}))
	defer srv.Close()
	defer UseTestServer(srv.Client().Transport, srv.URL)()

	b, err := RemoteModJSON(srv.URL + "/mod.zip")
	if err != nil || !strings.Contains(string(b), "2226697361") {
		t.Fatalf("%q %v", b, err)
	}
	if n := sent.Load(); n > 200<<10 {
		t.Errorf("read %d bytes of a %d-byte archive", n, len(data))
	}
	if _, err := RemoteModJSON(srv.URL + "/noranges.zip"); !errors.Is(err, ErrNoRanges) {
		t.Errorf("host without ranges: %v", err)
	}
}

type countingWriter struct {
	http.ResponseWriter
	n *atomic.Int64
}

func (c *countingWriter) Write(b []byte) (int, error) {
	c.n.Add(int64(len(b)))
	return c.ResponseWriter.Write(b)
}
