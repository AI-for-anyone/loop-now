package messageimage

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestInspectBoundedFormats(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	var p, j bytes.Buffer
	if e := png.Encode(&p, img); e != nil {
		t.Fatal(e)
	}
	if e := jpeg.Encode(&j, img, nil); e != nil {
		t.Fatal(e)
	}
	// A complete small lossless WebP fixture, decoded by the same production reader.
	webp, _ := base64.StdEncoding.DecodeString("UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA")
	for _, tc := range []struct {
		name, mime    string
		data          []byte
		limit, pixels int64
		valid         bool
	}{
		{"png", "image/png", p.Bytes(), DefaultBytes, 20, true}, {"jpeg", "image/jpeg", j.Bytes(), DefaultBytes, 20, true},
		{"webp", "image/webp", webp, DefaultBytes, 20, true}, {"mime", "image/jpeg", p.Bytes(), DefaultBytes, 20, false},
		{"svg", "image/svg+xml", []byte("<svg/>"), DefaultBytes, 20, false}, {"corrupt", "image/png", []byte("not an image"), DefaultBytes, 20, false},
		{"truncated", "image/png", p.Bytes()[:33], DefaultBytes, 20, false},
		{"bytes", "image/png", p.Bytes(), 1, 20, false}, {"pixels", "image/png", p.Bytes(), DefaultBytes, 5, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, e := Inspect(tc.data, tc.mime, tc.limit, tc.pixels)
			if (e == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, e)
			}
			if e == nil && (v.Size != int64(len(tc.data)) || v.MimeType != tc.mime) {
				t.Fatal(v)
			}
		})
	}
}
