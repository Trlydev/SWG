package stall

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A slow download that keeps sending finishes, past any overall limit the
// client has; one that goes silent is given up.
func TestDo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f := w.(http.Flusher)
		parts := 6
		if r.URL.Path == "/silent" {
			parts = 2
		}
		for i := 0; i < parts; i++ {
			w.Write([]byte("chunk"))
			f.Flush()
			time.Sleep(60 * time.Millisecond)
		}
		if r.URL.Path == "/silent" {
			time.Sleep(time.Second) // goes quiet
		}
	}))
	defer srv.Close()
	c := &http.Client{Timeout: 200 * time.Millisecond} // shorter than the whole download

	req, _ := http.NewRequest("GET", srv.URL+"/steady", nil)
	resp, err := Do(c, req, 150*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || len(b) != 30 {
		t.Fatalf("steady download: %d bytes, %v", len(b), err)
	}

	req, _ = http.NewRequest("GET", srv.URL+"/silent", nil)
	resp, err = Do(c, req, 150*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !errors.Is(err, ErrStalled) || time.Since(start) > 700*time.Millisecond {
		t.Errorf("silent download: %v after %s", err, time.Since(start))
	}
}
