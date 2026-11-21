package sources

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/nwaples/rardecode/v2"
)

// Reading a mod's mod.json without downloading the whole archive, from a
// host that serves byte ranges (Hugging Face, GameBanana's file cache):
// a zip keeps its table of contents at the end, so the tail is fetched,
// then only mod.json's bytes. A rar has no table at the end: it is read
// from the start and the download stops at mod.json. Used by the index of
// which copies are which Workshop item (cmd/copyindex).

// ErrNoRanges: the host doesn't serve byte ranges.
var ErrNoRanges = errors.New("the file host doesn't serve parts of files")

// ErrNoModJSON: the archive has no mod.json (a contraption, or not a mod).
var ErrNoModJSON = errors.New("no mod.json in the archive")

const maxModJSON = 1 << 20

// RemoteModJSON returns the mod.json (the shallowest one) of the zip or rar
// at u, reading as little of it as it can.
func RemoteModJSON(u string) ([]byte, error) {
	tail, size, err := getRange(u, -65558, 0)
	if err != nil {
		return nil, err
	}
	b, err := zipModJSON(u, tail, size)
	if errors.Is(err, errNotZip) {
		return rarModJSON(u) // not a zip: maybe a rar
	}
	return b, err
}

var errNotZip = errors.New("not a zip")

// getRange fetches bytes [from, from+n) of u (from < 0: the last -from
// bytes). It returns them and the file's total size.
func getRange(u string, from int64, n int64) ([]byte, int64, error) {
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, 0, err
	}
	setHeaders(req)
	if from < 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d", from))
	} else {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", from, from+n-1))
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return nil, 0, fmt.Errorf("%w (HTTP %d)", ErrNoRanges, resp.StatusCode)
	}
	total := int64(-1)
	if cr := resp.Header.Get("Content-Range"); cr != "" {
		if i := strings.LastIndexByte(cr, '/'); i >= 0 {
			total, _ = strconv.ParseInt(cr[i+1:], 10, 64)
		}
	}
	limit := n
	if from < 0 {
		limit = -from
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, 0, err
	}
	if int64(len(b)) > limit {
		return nil, 0, errors.New("the host sent more than was asked for")
	}
	return b, total, nil
}

type zipEntry struct {
	name         string
	method       uint16
	csize, usize uint64
	offset       uint64
}

// zipModJSON finds mod.json in the zip's table of contents (in or before
// tail, the file's last bytes) and fetches only its compressed bytes.
func zipModJSON(u string, tail []byte, size int64) ([]byte, error) {
	eocd := bytes.LastIndex(tail, []byte("PK\x05\x06"))
	if eocd < 0 || len(tail)-eocd < 22 {
		return nil, errNotZip
	}
	tailStart := size - int64(len(tail))
	cdSize := uint64(binary.LittleEndian.Uint32(tail[eocd+12:]))
	cdOff := uint64(binary.LittleEndian.Uint32(tail[eocd+16:]))
	if cdSize == 0xFFFFFFFF || cdOff == 0xFFFFFFFF {
		// zip64: the locator just before the end record points at the
		// zip64 end record, which holds the real sizes.
		loc := eocd - 20
		if loc < 0 || !bytes.Equal(tail[loc:loc+4], []byte("PK\x06\x07")) {
			return nil, errors.New("zip64 archive without its locator")
		}
		recOff := int64(binary.LittleEndian.Uint64(tail[loc+8:]))
		rec, err := bytesAt(u, tail, tailStart, recOff, 56)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(rec[:4], []byte("PK\x06\x06")) {
			return nil, errors.New("zip64 end record not found")
		}
		cdSize = binary.LittleEndian.Uint64(rec[40:])
		cdOff = binary.LittleEndian.Uint64(rec[48:])
	}
	if cdSize > 16<<20 {
		return nil, errors.New("zip table of contents too large")
	}
	cd, err := bytesAt(u, tail, tailStart, int64(cdOff), int64(cdSize))
	if err != nil {
		return nil, err
	}
	var best *zipEntry
	for p := 0; p+46 <= len(cd); {
		if !bytes.Equal(cd[p:p+4], []byte("PK\x01\x02")) {
			break
		}
		e := zipEntry{
			method: binary.LittleEndian.Uint16(cd[p+10:]),
			csize:  uint64(binary.LittleEndian.Uint32(cd[p+20:])),
			usize:  uint64(binary.LittleEndian.Uint32(cd[p+24:])),
			offset: uint64(binary.LittleEndian.Uint32(cd[p+42:])),
		}
		nl := int(binary.LittleEndian.Uint16(cd[p+28:]))
		el := int(binary.LittleEndian.Uint16(cd[p+30:]))
		cl := int(binary.LittleEndian.Uint16(cd[p+32:]))
		if p+46+nl+el+cl > len(cd) {
			break
		}
		e.name = strings.ReplaceAll(string(cd[p+46:p+46+nl]), `\`, "/")
		zip64Sizes(&e, cd[p+46+nl:p+46+nl+el])
		p += 46 + nl + el + cl
		if !strings.EqualFold(path.Base(e.name), "mod.json") {
			continue
		}
		if best == nil || strings.Count(e.name, "/") < strings.Count(best.name, "/") {
			ee := e
			best = &ee
		}
	}
	if best == nil {
		return nil, ErrNoModJSON
	}
	if best.usize > maxModJSON || best.csize > maxModJSON {
		return nil, errors.New("mod.json too large")
	}
	lh, err := bytesAt(u, tail, tailStart, int64(best.offset), 30)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(lh[:4], []byte("PK\x03\x04")) {
		return nil, errors.New("zip entry header not found")
	}
	dataOff := int64(best.offset) + 30 + int64(binary.LittleEndian.Uint16(lh[26:])) + int64(binary.LittleEndian.Uint16(lh[28:]))
	data, err := bytesAt(u, tail, tailStart, dataOff, int64(best.csize))
	if err != nil {
		return nil, err
	}
	switch best.method {
	case 0:
		return data, nil
	case 8:
		return io.ReadAll(io.LimitReader(flate.NewReader(bytes.NewReader(data)), maxModJSON))
	}
	return nil, fmt.Errorf("mod.json is compressed with method %d", best.method)
}

// zip64Sizes reads the zip64 extra field of a table entry.
func zip64Sizes(e *zipEntry, extra []byte) {
	for len(extra) >= 4 {
		id := binary.LittleEndian.Uint16(extra)
		n := int(binary.LittleEndian.Uint16(extra[2:]))
		if 4+n > len(extra) {
			return
		}
		if id == 1 {
			f := extra[4 : 4+n]
			next := func(v *uint64) {
				if *v == 0xFFFFFFFF && len(f) >= 8 {
					*v = binary.LittleEndian.Uint64(f)
					f = f[8:]
				}
			}
			next(&e.usize)
			next(&e.csize)
			next(&e.offset)
			return
		}
		extra = extra[4+n:]
	}
}

// bytesAt returns n bytes at off, from tail when it holds them (tail starts
// at tailStart), else with another range request.
func bytesAt(u string, tail []byte, tailStart, off, n int64) ([]byte, error) {
	if off >= tailStart && off+n <= tailStart+int64(len(tail)) {
		return tail[off-tailStart : off-tailStart+n], nil
	}
	if off < 0 || n <= 0 {
		return nil, errors.New("bad zip offsets")
	}
	b, _, err := getRange(u, off, n)
	if err != nil {
		return nil, err
	}
	if int64(len(b)) != n {
		return nil, errors.New("short read from the file host")
	}
	return b, nil
}

// rarModJSON reads a rar from the start and stops at its mod.json (a rar
// lists each file just before its data), reading at most 64 MB.
func rarModJSON(u string) ([]byte, error) {
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	setHeaders(req)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", u, resp.StatusCode)
	}
	rr, err := rardecode.NewReader(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("not a zip or rar: %w", err)
	}
	var best []byte
	bestDepth := 1 << 30
	for {
		h, err := rr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			if best != nil {
				break
			}
			return nil, err
		}
		name := strings.ReplaceAll(h.Name, `\`, "/")
		if h.IsDir || !strings.EqualFold(path.Base(name), "mod.json") {
			continue
		}
		if d := strings.Count(name, "/"); d < bestDepth {
			b, err := io.ReadAll(io.LimitReader(rr, maxModJSON))
			if err != nil {
				return nil, err
			}
			best, bestDepth = b, d
			if d <= 1 {
				break // a mod's own folder: no shallower one to wait for
			}
		}
	}
	if best == nil {
		return nil, ErrNoModJSON
	}
	return best, nil
}
