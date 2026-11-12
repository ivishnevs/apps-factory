package preview

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

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
