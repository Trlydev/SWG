package workshop

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	_ "image/gif" // decoders for mod thumbnails
	"image/jpeg"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// Thumbnails for the index: the mod's own preview image, taken out of its
// file once when it is published (or by the daily refresh), shrunk, and
// kept on the data branch under a name made from its hash. The index links
// to it, so the app shows Open Workshop cards without downloading the mods.

const (
	thumbMaxW, thumbMaxH = 640, 400
	thumbMaxBytes        = 8 << 20
	thumbMaxPixels       = 40e6
)

// ThumbURL is where the index links a thumbnail kept on the data branch.
func ThumbURL(name string) string {
	return "https://raw.githubusercontent.com/" + Repo + "/" + DataBranch + "/thumbs/" + name
}

// ThumbFrom finds the preview image in an unpacked mod or contraption (dir)
// and returns it shrunk to fit 640x400, as JPEG (PNG when it has
// transparency), with the file name to keep it under; nil if there is none.
func ThumbFrom(dir string) (data []byte, name string) {
	for _, c := range thumbCandidates(dir) {
		b, err := os.ReadFile(c)
		if err != nil || len(b) > thumbMaxBytes {
			continue
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
		if err != nil || cfg.Width*cfg.Height > thumbMaxPixels || cfg.Width == 0 || cfg.Height == 0 {
			continue
		}
		img, _, err := image.Decode(bytes.NewReader(b))
		if err != nil {
			continue
		}
		img = shrinkThumb(img)
		var buf bytes.Buffer
		ext := ".jpg"
		if o, ok := img.(interface{ Opaque() bool }); ok && !o.Opaque() {
			ext, err = ".png", png.Encode(&buf, img)
		} else {
			err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85})
		}
		if err != nil {
			continue
		}
		h := sha256.Sum256(buf.Bytes())
		return buf.Bytes(), hex.EncodeToString(h[:16]) + ext
	}
	return nil, ""
}

// thumbCandidates lists where a preview image may be, best first: the
// mod.json's ThumbnailPath, the usual names next to it, then (for a
// contraption) its first .png.
func thumbCandidates(dir string) []string {
	var out, pngs []string
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		switch {
		case strings.EqualFold(d.Name(), "mod.json"):
			root := filepath.Dir(p)
			var mj struct {
				ThumbnailPath string `json:"ThumbnailPath"`
			}
			if b, err := os.ReadFile(p); err == nil && json.Unmarshal(b, &mj) == nil && mj.ThumbnailPath != "" {
				rel := filepath.Clean(filepath.FromSlash(strings.ReplaceAll(mj.ThumbnailPath, `\`, "/")))
				if !filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					out = append(out, filepath.Join(root, rel))
				}
			}
			for _, n := range []string{"thumb.png", "thumbnail.png", "thumb.jpg", "icon.png"} {
				out = append(out, filepath.Join(root, n))
			}
		case strings.EqualFold(filepath.Ext(p), ".png"):
			pngs = append(pngs, p)
		}
		return nil
	})
	return append(out, pngs...)
}

func shrinkThumb(img image.Image) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= thumbMaxW && h <= thumbMaxH {
		return img
	}
	scale := min(float64(thumbMaxW)/float64(w), float64(thumbMaxH)/float64(h))
	dst := image.NewRGBA(image.Rect(0, 0, max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	return dst
}
