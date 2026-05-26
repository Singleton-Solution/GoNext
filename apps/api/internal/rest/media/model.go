package media

import (
	"context"
	"errors"
	"time"
)

const (
	DefaultListLimit = 30
	MaxListLimit     = 100
)

// Asset is the public wire shape. Mirrors admin/media.Asset minus the
// uploader id and the SHA. See the package doc for the rationale.
type Asset struct {
	ID        string    `json:"id"`
	Filename  string    `json:"filename"`
	MimeType  string    `json:"mime_type"`
	ByteSize  int64     `json:"byte_size"`
	Width     *int      `json:"width,omitempty"`
	Height    *int      `json:"height,omitempty"`
	AltText   string    `json:"alt_text"`
	Caption   string    `json:"caption"`
	PublicURL string    `json:"public_url,omitempty"`
	Variants  []Variant `json:"variants,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Variant mirrors admin/media.Variant for the renderer's responsive
// image source set. PublicURL is computed by the store the same way
// the admin surface computes its own.
type Variant struct {
	Name      string `json:"name"`
	Format    string `json:"format"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	MimeType  string `json:"mime_type"`
	PublicURL string `json:"public_url,omitempty"`
}

// ListFilter narrows the GET /api/v1/media response.
type ListFilter struct {
	// MimeClass is one of "", "image", "video", "document". Empty
	// matches all.
	MimeClass string
	Limit     int
	After     string
}

// Store is the public read-only persistence boundary. It's distinct
// from admin/media.Store on purpose: there's no Insert, no Update,
// no SoftDelete — the public surface can only read.
type Store interface {
	List(ctx context.Context, f ListFilter) ([]Asset, error)
	GetByID(ctx context.Context, id string) (Asset, error)
}

// ErrNotFound is the sentinel returned by store reads when the row is
// missing (or soft-deleted). The handler maps to HTTP 404.
var ErrNotFound = errors.New("rest/media: not found")
