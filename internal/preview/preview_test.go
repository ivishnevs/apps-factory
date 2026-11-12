package preview

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

// pngHeader is the start of a PNG claiming w x h pixels (enough for DecodeConfig).
func pngHeader(w, h uint32) []byte {
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8], ihdr[9] = 16, 6 // 16-bit RGBA: 8 bytes per pixel decoded
	chunk := append([]byte("IHDR"), ihdr...)
	out := []byte("\x89PNG\r\n\x1a\n")
	out = binary.BigEndian.AppendUint32(out, 13)
	out = append(out, chunk...)
	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(chunk))
}

// TestRenderPixelBound (security-review-2026-11 F5): an image above maxPixels is never decoded (a 48 Mpx 16-bit PNG
// would take ~400 MB); a 24 Mpx photo is within it.
func TestRenderPixelBound(t *testing.T) {
	if cfg, err := png.DecodeConfig(bytes.NewReader(pngHeader(8000, 6000))); err != nil || cfg.Width != 8000 {
		t.Fatal("test header", cfg, err)
	}
	if _, ok := Render("huge.png", pngHeader(8000, 6000)); ok {
		t.Fatal("a 48 Mpx image was rendered")
	}
	if int64(8000)*6000 <= maxPixels || int64(6000)*4000 > maxPixels {
		t.Fatal("maxPixels", maxPixels)
	}
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestRender(t *testing.T) {
	// A large opaque image gets a JPEG within MaxSide, aspect ratio kept.
	big := image.NewRGBA(image.Rect(0, 0, 3200, 1000))
	for i := range big.Pix {
		big.Pix[i] = 0xff
	}
	big.Set(10, 10, color.RGBA{255, 0, 0, 255})
	img, ok := Render("towers/side_by_side.png", encodePNG(t, big))
	if !ok || img.Width != 1600 || img.Height != 500 || img.Ext != ".jpg" {
		t.Fatalf("rendition %v %dx%d %s", ok, img.Width, img.Height, img.Ext)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(img.Data))
	if err != nil || cfg.Width != 1600 || cfg.Height != 500 {
		t.Fatalf("decoded rendition %+v %v", cfg, err)
	}

	// A tall image with transparency stays PNG and keeps its alpha.
	tall := image.NewNRGBA(image.Rect(0, 0, 400, 2000))
	tall.Set(0, 0, color.NRGBA{0, 0, 255, 128})
	img, ok = Render("icon.PNG", encodePNG(t, tall))
	if !ok || img.Width != 320 || img.Height != 1600 || img.Ext != ".png" {
		t.Fatalf("transparent rendition %v %dx%d %s", ok, img.Width, img.Height, img.Ext)
	}

	// A small image, another format or something that is not an image needs none.
	if _, ok := Render("small.png", encodePNG(t, image.NewRGBA(image.Rect(0, 0, 64, 64)))); ok {
		t.Fatal("small image rendered")
	}
	if _, ok := Render("shot.webp", []byte("RIFF....WEBP")); ok {
		t.Fatal("webp rendered")
	}
	if _, ok := Render("broken.jpg", []byte("not an image")); ok {
		t.Fatal("broken image rendered")
	}
}
