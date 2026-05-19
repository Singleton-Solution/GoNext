//go:build !avif

package imageproc

import "image"

// encodeAVIF is the build-default AVIF encoder: a no-op that returns
// a Warning instructing the caller to fall back to WebP. The cgo-
// backed implementation lives in avif_libaom.go behind the `avif`
// build tag.
//
// # Why a stub
//
// github.com/Kagami/go-avif links against libaom via cgo. A repo-
// wide `go test ./...` on a CI runner without libaom would fail to
// link; this stub keeps the pipeline portable. Deployments that ship
// libaom build the binary with `-tags avif` to get real AVIF
// encoding; the storage layout is identical so a fleet rolling
// upgrade can flip the flag without touching the keys.
func encodeAVIF(img image.Image, quality int) ([]byte, string, error) {
	_ = img
	_ = quality
	return nil, "AVIF encoder not built (rebuild with -tags avif for libaom support); served WebP fallback", nil
}
