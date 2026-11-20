package sources

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestSteamDetails(t *testing.T) {
	asked := 0
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked++
		b, _ := io.ReadAll(r.Body)
		q, _ := url.ParseQuery(string(b))
		if !strings.HasSuffix(r.URL.Path, "/ISteamRemoteStorage/GetPublishedFileDetails/v1/") || q.Get("itemcount") != "2" {
			t.Errorf("asked %s %v", r.URL.Path, q)
		}
		w.Write([]byte(`{"response":{"publishedfiledetails":[
			{"publishedfileid":"3838486260","result":1,"title":" Violet ","time_updated":1794501569,"consumer_app_id":1118200,"banned":0},
			{"publishedfileid":"3351798392","result":9}]}}`))
	})
	defer UseTestServer(stubRT{h}, "http://unused")()
	old := SteamAPI
	SteamAPI = "http://unused"
	defer func() { SteamAPI = old }()
	steamMu.Lock()
	steamCache = map[string]steamCached{}
	steamMu.Unlock()

	m, err := SteamDetails([]string{"3838486260", "3351798392", "not-an-id"})
	if err != nil {
		t.Fatal(err)
	}
	if v := m["3838486260"]; !v.Live || v.Title != "Violet" || v.Updated.Unix() != 1794501569 {
		t.Errorf("live item: %+v", v)
	}
	if v := m["3351798392"]; v.Live {
		t.Errorf("removed item reported live: %+v", v)
	}
	if it, ok := SteamItemByID("3838486260"); !ok || it.Title != "Violet" || asked != 1 {
		t.Errorf("cached lookup: %+v %v (asked %d times)", it, ok, asked)
	}
	if _, ok := SteamItemByID("3351798392"); ok {
		t.Error("removed item found")
	}
}
