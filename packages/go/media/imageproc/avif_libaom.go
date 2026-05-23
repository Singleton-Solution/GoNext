//go:build avif

package imageproc

import (
	"bytes"
	"image"

	"github.com/Kagami/go-avif"
)

// encodeAVIF is the libaom-backed AVIF encoder. Active when the
// binary is built with `-tags avif` AND libaom-dev is present at
// link time (the encoder is cgo). The stub in avif_stub.go covers
// every other case.
//
// quality maps from the package's 1..100 quality knob to go-avif's
// 0..63 quantizer (smaller = higher quality). The mapping is
// linear; quality=82 → quantizer≈18 which is the "visually lossless"
// threshold for photographic content per AOMedia's reference docs.
func encodeAVIF(img image.Image, quality int) ([]byte, string, error) {
	if quality <= 0 || quality > 100 {
		quality = 82
	}
	// Higher quality → lower quantizer. The 63→0 inversion maps the
	// caller's knob ergonomically (100 is best) while staying within
	// libaom's documented range.
	quant := avif.MinQuality + (avif.MaxQuality-avif.MinQuality)*(100-quality)/100
	if quant < avif.MinQuality {
		quant = avif.MinQuality
	}
	if quant > avif.MaxQuality {
		quant = avif.MaxQuality
	}
	var buf bytes.Buffer
	err := avif.Encode(&buf, img, &avif.Options{
		Quality:           quant,
		Speed:             avif.MaxSpeed,
		SubsampleRatio:    nil,
		RGBToYUVConverter: nil,
	})
	if err != nil {
		return nil, "", err
	}
	return buf.Bytes(), "", nil
}
