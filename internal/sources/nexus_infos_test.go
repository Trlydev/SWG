package sources

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A preview Nexus can't serve is remembered: the next lookups don't ask
// again (every mod's mirror lookup classifies all Nexus uploads), until
// nxRetryFailed has passed.
func TestNXInfosRemembersFailures(t *testing.T) {
	var previews atomic.Int32
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "graphql"):
			w.Write([]byte(`{"data":{"m1":[{"uri":"a-1.zip","category":"MAIN","date":1,"fileId":10}],"m2":[{"uri":"14/dd/92/b-2","category":"MAIN","date":1,"fileId":20}]}}`))
		case strings.Contains(r.URL.Host, "file-metadata"):
			previews.Add(1)
			if !strings.HasPrefix(r.URL.Path, "/file/production-nexus-files-s3-meta/4138/") {
				t.Errorf("preview asked at %s", r.URL.Path)
			}
			if strings.Contains(r.URL.Path, "/1/") {
				w.Write([]byte(`{"children":[{"path":"2091325214_x/mod.json","type":"file"}]}`))
				return
			}
			if r.URL.EscapedPath() != "/file/production-nexus-files-s3-meta/4138/2/14/dd/92/b-2.json" {
				t.Errorf("newer file's preview asked at %s", r.URL.EscapedPath())
			}
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	})
	defer UseTestServer(stubRT{h}, "http://unused")()
	oldDir := IndexCacheDir
	IndexCacheDir = t.TempDir()
	nxInfoMu.Lock()
	nxInfos = nil
	nxInfoMu.Unlock()
	nxFilesMu.Lock()
	nxFiles = map[int]nxFileEntry{}
	nxFilesMu.Unlock()
	t.Cleanup(func() {
		IndexCacheDir = oldDir
		nxInfoMu.Lock()
		nxInfos = nil
		nxInfoMu.Unlock()
	})

	mods := []NXMod{{ID: 1}, {ID: 2}}
	got := NXInfos(mods)
	if got[1].WorkshopID != "2091325214" || got[1].Failed {
		t.Errorf("readable preview: %+v", got[1])
	}
	if _, ok := got[2]; ok {
		t.Errorf("a failed preview was returned: %+v", got[2])
	}
	if n := previews.Load(); n != 2 {
		t.Fatalf("%d preview requests, want 2", n)
	}
	NXInfos(mods)
	if n := previews.Load(); n != 2 {
		t.Errorf("asked again right away: %d requests", n)
	}

	// After nxRetryFailed, the failed one (only) is asked again.
	nxInfoMu.Lock()
	in := nxInfos[2]
	in.At = time.Now().Add(-nxRetryFailed - time.Minute)
	nxInfos[2] = in
	nxInfoMu.Unlock()
	NXInfos(mods)
	if n := previews.Load(); n != 3 {
		t.Errorf("after the retry period: %d requests, want 3", n)
	}
}
