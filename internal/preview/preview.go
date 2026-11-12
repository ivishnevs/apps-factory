// Package preview renders display-size renditions of large images (inbox-visual-previews 2.2): the inbox, the
// chat and Telegram show the rendition, the original stays downloadable. Standard library only: PNG, JPEG and
// GIF are rendered; other formats (WebP, SVG, AVIF) are shown as they are.
package preview

import (
	"bytes"
	"image"
	"image/draw"
	_ "image/gif" // registers the GIF decoder
	"image/jpeg"
	"image/png"
	"path"
	"strings"
)

// renders lets one image decode at a time in the process: a decode takes up to maxPixels x 8 bytes (16-bit PNG), and
// agents of every company attach images (security-review-2026-11 F5).
var renders = make(chan struct{}, 1)

const (
	// MaxSide is the longest side of a rendition.
	MaxSide = 1600
	// MaxBytes: an image within MaxSide and at most this size needs no rendition.
	MaxBytes = 1 << 20
	// maxPixels bounds the images decoded at all (memory: 4 bytes per pixel, 8 for a 16-bit PNG): 40 Mpx covers
	// phone and camera photos; a larger image is shown as it is (security-review-2026-11 F5).
	maxPixels = 40 << 20
)

// Renderable tells whether name is an image format this package renders.
func Renderable(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif":
		return true
	}
	return false
}

// Image is a rendition: its encoded bytes (JPEG for an opaque image, PNG otherwise) and size in pixels.
type Image struct {
	Data          []byte
	Width, Height int
	Ext           string
}

// Render makes a rendition of b when it is a renderable image larger than MaxSide or MaxBytes; ok is false when
// none is needed or the image cannot be rendered (it is then shown as it is).
func Render(name string, b []byte) (Image, bool) {
	if !Renderable(name) {
		return Image{}, false
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return Image{}, false
	}
	if cfg.Width <= MaxSide && cfg.Height <= MaxSide && len(b) <= MaxBytes {
		return Image{}, false
	}
	renders <- struct{}{}
	defer func() { <-renders }()
	src, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return Image{}, false
	}
	w, h := fit(cfg.Width, cfg.Height)
	dst := scale(src, w, h)
	var out bytes.Buffer
	img := Image{Width: w, Height: h, Ext: ".png"}
	if dst.Opaque() {
		img.Ext = ".jpg"
		err = jpeg.Encode(&out, dst, &jpeg.Options{Quality: 85})
	} else {
		err = (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&out, dst)
	}
	if err != nil || out.Len() >= len(b) && w == cfg.Width && h == cfg.Height {
		return Image{}, false
	}
	img.Data = out.Bytes()
	return img, true
}

// fit is the size of w x h within MaxSide, keeping the aspect ratio.
func fit(w, h int) (int, int) {
	if w <= MaxSide && h <= MaxSide {
		return w, h
	}
	if w >= h {
		return MaxSide, max(1, h*MaxSide/w)
	}
	return max(1, w*MaxSide/h), MaxSide
}

// scale shrinks src to w x h by averaging the source pixels each target pixel covers (a box filter over
// premultiplied colour, so transparent edges do not darken).
func scale(src image.Image, w, h int) *image.RGBA {
	b := src.Bounds()
	rgba, ok := src.(*image.RGBA)
	if !ok || rgba.Bounds().Min != (image.Point{}) {
		rgba = image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
		draw.Draw(rgba, rgba.Bounds(), src, b.Min, draw.Src)
	}
	sw, sh := rgba.Bounds().Dx(), rgba.Bounds().Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	if sw == w && sh == h {
		copy(dst.Pix, rgba.Pix)
		return dst
	}
	for y := 0; y < h; y++ {
		y0, y1 := y*sh/h, max((y+1)*sh/h, y*sh/h+1)
		for x := 0; x < w; x++ {
			x0, x1 := x*sw/w, max((x+1)*sw/w, x*sw/w+1)
			var r, g, bl, a, n uint64
			for sy := y0; sy < y1; sy++ {
				row := rgba.Pix[sy*rgba.Stride:]
				for sx := x0; sx < x1; sx++ {
					p := row[sx*4 : sx*4+4]
					r, g, bl, a = r+uint64(p[0]), g+uint64(p[1]), bl+uint64(p[2]), a+uint64(p[3])
					n++
				}
			}
			o := dst.Pix[y*dst.Stride+x*4:]
			o[0], o[1], o[2], o[3] = uint8(r/n), uint8(g/n), uint8(bl/n), uint8(a/n)
		}
	}
	return dst
}
