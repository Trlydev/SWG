// Command copyindex builds the copies index (internal/copies): for every
// True Workshop and GameBanana People Playground upload, the Steam Workshop
// item its mod.json names. It reads only a few KB of each upload
// (sources.RemoteModJSON) and only uploads that are new or changed since the
// last index, so a daily run is short. Run by the "copies" GitHub Action.
//
//	go run ./cmd/copyindex -dir data
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Trlydev/SWG/internal/copies"
	"github.com/Trlydev/SWG/internal/manager"
	"github.com/Trlydev/SWG/internal/sources"
)

func main() {
	dir := flag.String("dir", "data", "folder holding copies.json (read, then written)")
	flag.Parse()
	path := filepath.Join(*dir, "copies.json")
	prev := &copies.Index{Items: map[string]copies.Entry{}}
	if b, err := os.ReadFile(path); err == nil {
		if ix, err := copies.Parse(b); err == nil {
			prev = ix
		}
	}
	next := &copies.Index{Generated: time.Now().UTC(), Items: map[string]copies.Entry{}}
	var mu sync.Mutex
	set := func(ref string, e copies.Entry) {
		mu.Lock()
		next.Items[ref] = e
		mu.Unlock()
	}
	type job struct {
		ref, url string
		e        copies.Entry
	}
	var jobs []job

	tw, err := sources.TWAll()
	if err != nil {
		log.Fatalf("True Workshop: %v", err)
	}
	for _, it := range tw {
		ref := fmt.Sprintf("tw:%d", it.ID)
		e := copies.Entry{File: it.SHA256}
		if old, ok := prev.Items[ref]; ok && old.File == e.File && !(old.WS != "" && old.Name == "") {
			set(ref, old) // the same file: nothing to read
			continue
		}
		if it.Type != "mod" {
			set(ref, e) // a contraption has no mod.json
			continue
		}
		jobs = append(jobs, job{ref, it.DownloadURL(), e})
	}

	gb, err := sources.GBAll()
	if err != nil {
		log.Fatalf("GameBanana: %v", err)
	}
	for _, m := range gb {
		ref := fmt.Sprintf("gb:%d", m.ID)
		if old, ok := prev.Items[ref]; ok && old.Mod == m.Modified && !(old.WS != "" && old.Name == "") {
			set(ref, old) // unchanged since it was read
			continue
		}
		files, err := sources.GBFiles(m.ID)
		if err != nil || len(files) == 0 {
			continue // tried again next run
		}
		f := files[0]
		e := copies.Entry{File: fmt.Sprint(f.ID), Mod: m.Modified}
		if old, ok := prev.Items[ref]; ok && old.File == e.File && !(old.WS != "" && old.Name == "") {
			old.Mod = m.Modified
			set(ref, old)
			continue
		}
		jobs = append(jobs, job{ref, f.DownloadURL, e})
	}

	log.Printf("%d uploads known, %d to read", len(next.Items), len(jobs))
	sem := make(chan struct{}, 4) // gentle on the file hosts
	var wg sync.WaitGroup
	read, named := 0, 0
	for _, j := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func(j job) {
			defer wg.Done()
			defer func() { <-sem }()
			b, err := sources.RemoteModJSON(j.url)
			switch {
			case err == nil:
				j.e.WS, j.e.Name = workshopID(b)
			case err == sources.ErrNoModJSON:
			case strings.Contains(err.Error(), "not a zip or rar"):
				// 7z and other formats can't be read in part: noted, so they
				// aren't tried every day (again once the file changes).
			default:
				log.Printf("%s: %v (tried again next run)", j.ref, err)
				return
			}
			set(j.ref, j.e)
			mu.Lock()
			read++
			if j.e.WS != "" {
				named++
			}
			mu.Unlock()
		}(j)
	}
	wg.Wait()
	log.Printf("read %d mod.json files, %d name a Workshop item; %d uploads in the index", read, named, len(next.Items))

	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetIndent("", " ")
	if err := enc.Encode(next); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(*dir, 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
		log.Fatal(err)
	}
}

// workshopID is the Workshop item a mod.json names (CreatorUGCIdentity)
// and the mod's Name.
func workshopID(b []byte) (ws, name string) {
	var mj struct {
		Name               string          `json:"Name"`
		CreatorUGCIdentity json.RawMessage `json:"CreatorUGCIdentity"`
	}
	if json.Unmarshal(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")), &mj) != nil {
		return "", ""
	}
	ws = manager.UGCString(mj.CreatorUGCIdentity)
	if len(ws) < 6 || len(ws) > 12 || strings.Trim(ws, "0123456789") != "" {
		return "", ""
	}
	name = strings.TrimSpace(mj.Name)
	if len(name) > 200 {
		name = name[:200]
	}
	return ws, name
}
