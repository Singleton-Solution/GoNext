// Package sign implements cryptographic identity verification for
// .gnplugin bundles using sigstore/cosign keyless signatures, with an
// air-gapped keyed-cosign fallback.
//
// The plugins/manifest package validates the *shape* of a bundle. This
// package validates *who* signed it. The host's Install path runs sign
// verification before any storage write, so a bundle that fails identity
// verification never lands in the lifecycle state machine.
//
// Two modes are supported: keyless (Fulcio-issued, Rekor-logged) and
// keyed (long-lived cosign keypair, COSIGN_PUBLIC_KEY trust root). The
// publisher's declared identity lives in signatures/publisher.json
// inside the bundle; the cosign sig blob + bundle live next to it.
//
// Verified bundles are cached by canonical digest so repeated installs
// of the same bundle don't re-hit Rekor.
package sign
