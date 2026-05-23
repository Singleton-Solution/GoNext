// Package marketplace exposes the public-facing browse + install
// surface for the plugin marketplace under
// /api/v1/admin/marketplace.
//
// The data model itself lives in packages/go/plugins/marketplace
// (PR #396) and is shared with the publisher / moderator surface.
// This package is the read-side projection an authenticated admin
// uses to:
//
//   - browse listings with category/search/sort filters,
//   - inspect a single listing's detail + version history +
//     compatibility matrix,
//   - see and submit star ratings + reviews,
//   - install the latest compatible version through the existing
//     plugin lifecycle.Manager.
//
// Auth posture
// ============
//
//   - Read endpoints (list, detail, versions, ratings GET) require
//     a logged-in principal but no special capability — every
//     operator can browse the catalogue.
//   - Install + rating POST require the plugins.install capability
//     (CapInstallPlugins) so a constrained admin role can browse but
//     can't add software to the host.
//
// Object-store coupling
// =====================
//
// Listings carry only a sha256 digest of the wasm artefact; the
// bytes themselves live in object storage keyed by that digest.
// The install path fetches the bundle through a BundleFetcher
// (interface in this package) so the production wiring can point
// at MinIO/S3 while tests use an in-memory map.
//
// Versions returned to clients hide the raw wasm digest as a hex
// string — the digest is operationally interesting (lets an
// operator verify what's about to be installed) but not the bytes.
package marketplace
