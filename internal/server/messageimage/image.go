// Package messageimage contains only bounded visual message inputs, not arbitrary attachments.
package messageimage

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"github.com/google/uuid"
	_ "golang.org/x/image/webp"
	"image"
	_ "image/jpeg"
	_ "image/png"
)

const DefaultCount = 4
const DefaultBytes int64 = 5 << 20
const DefaultPixels int64 = 20_000_000

type Ref struct {
	ID       uuid.UUID `json:"id"`
	MimeType string    `json:"mimeType"`
	Size     int64     `json:"size"`
	Width    int       `json:"width"`
	Height   int       `json:"height"`
}
type Content struct {
	Type     string `json:"type"`
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
}

func Encode(mime string, data []byte) Content {
	return Content{Type: "image", MimeType: mime, Data: base64.StdEncoding.EncodeToString(data)}
}
func Inspect(data []byte, mime string, maxBytes, maxPixels int64) (Ref, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultBytes
	}
	if maxPixels <= 0 {
		maxPixels = DefaultPixels
	}
	if len(data) == 0 || int64(len(data)) > maxBytes {
		return Ref{}, fmt.Errorf("图片字节超限")
	}
	if mime != "image/png" && mime != "image/jpeg" && mime != "image/webp" {
		return Ref{}, fmt.Errorf("仅支持PNG、JPEG、WebP")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Ref{}, fmt.Errorf("图片内容无效")
	}
	expected := map[string]string{"png": "image/png", "jpeg": "image/jpeg", "webp": "image/webp"}[format]
	if expected != mime || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width) > maxPixels/int64(cfg.Height) {
		return Ref{}, fmt.Errorf("图片格式或像素超限")
	}
	if _, decodedFormat, e := image.Decode(bytes.NewReader(data)); e != nil || decodedFormat != format {
		return Ref{}, fmt.Errorf("图片内容无效")
	}
	return Ref{MimeType: mime, Size: int64(len(data)), Width: cfg.Width, Height: cfg.Height}, nil
}
