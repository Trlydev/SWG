package workshop

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func pngOf(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		img.Set(x, 0, color.RGBA{255, 0, 0, 255})
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 9, 255})
		}
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

// The preview named in mod.json is taken (shrunk to fit 640x400); a path
// leaving the mod folder is ignored; no image, no thumbnail.
func TestThumbFrom(t *testing.T) {
	dir := t.TempDir()
	mod := filepath.Join(dir, "Plane")
	os.MkdirAll(filepath.Join(mod, "sprites"), 0o755)
	os.WriteFile(filepath.Join(mod, "mod.json"), []byte(`{"Name":"Plane","ThumbnailPath":"sprites/preview.png"}`), 0o644)
	os.WriteFile(filepath.Join(mod, "sprites", "preview.png"), pngOf(1920, 1080), 0o644)
	b, name := ThumbFrom(dir)
	if b == nil || !strings.HasSuffix(name, ".jpg") {
		t.Fatalf("no thumbnail: %q", name)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || cfg.Width > 640 || cfg.Height > 400 {
		t.Errorf("not shrunk: %+v %v", cfg, err)
	}
	if !strings.HasPrefix(ThumbURL(name), "https://raw.githubusercontent.com/Trlydev/SWG/workshop-data/thumbs/") {
		t.Errorf("url: %s", ThumbURL(name))
	}

	evil := t.TempDir()
	os.WriteFile(filepath.Join(filepath.Dir(evil), "outside.png"), pngOf(10, 10), 0o644)
	os.WriteFile(filepath.Join(evil, "mod.json"), []byte(`{"ThumbnailPath":"../outside.png"}`), 0o644)
	if b, _ := ThumbFrom(evil); b != nil {
		t.Error("took an image from outside the mod folder")
	}
}
