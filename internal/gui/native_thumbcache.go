//go:build cgo && !nofyne

package gui

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/image/draw"

	"github.com/Trlydev/SWG/internal/manager"
)

// Thumbnails are shrunk once after download and kept on disk, so the window
// draws small images and doesn't download them again on the next start.

const (
	thumbMaxW, thumbMaxH = 640, 400 // sharp in the 128 px cards on HiDPI and in the 300 px details image
	thumbDiskTTL         = 14 * 24 * time.Hour
)

// shrink scales img down to fit thumbMaxW×thumbMaxH (never up).
func shrink(img image.Image) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= thumbMaxW && h <= thumbMaxH || w == 0 || h == 0 {
		return img
	}
	scale := min(float64(thumbMaxW)/float64(w), float64(thumbMaxH)/float64(h))
	nw, nh := max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	return dst
}

// thumbDiskPath is where a downloaded thumbnail is kept ("" if unknown).
func thumbDiskPath(src string) string {
	d, err := manager.ConfigDir()
	if err != nil {
		return ""
	}
	h := sha256.Sum256([]byte(src))
	return filepath.Join(d, "cache", "thumbs", hex.EncodeToString(h[:16]))
}

// readThumbDisk returns a kept thumbnail's bytes, if fresh.
func readThumbDisk(src string) []byte {
	p := thumbDiskPath(src)
	if p == "" {
		return nil
	}
	fi, err := os.Stat(p)
	if err != nil || time.Since(fi.ModTime()) > thumbDiskTTL || fi.Size() > 4<<20 {
		return nil
	}
	b, _ := os.ReadFile(p)
	return b
}

// writeThumbDisk keeps a (shrunk) thumbnail: JPEG, or PNG when it has
// transparency.
func writeThumbDisk(src string, img image.Image) {
	p := thumbDiskPath(src)
	if p == "" {
		return
	}
	var buf bytes.Buffer
	var err error
	if o, ok := img.(interface{ Opaque() bool }); ok && !o.Opaque() {
		err = png.Encode(&buf, img)
	} else {
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85})
	}
	if err != nil || os.MkdirAll(filepath.Dir(p), 0o700) != nil {
		return
	}
	tmp := p + ".tmp"
	if os.WriteFile(tmp, buf.Bytes(), 0o600) == nil {
		os.Rename(tmp, p)
	}
}

// pruneThumbDisk deletes kept thumbnails past their time (run now and then).
func pruneThumbDisk() {
	p := thumbDiskPath("x")
	if p == "" {
		return
	}
	ents, _ := os.ReadDir(filepath.Dir(p))
	for _, e := range ents {
		if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > thumbDiskTTL {
			os.Remove(filepath.Join(filepath.Dir(p), e.Name()))
		}
	}
}
