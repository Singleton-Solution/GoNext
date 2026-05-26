// Package videoproc is the upload-time video transcoding pipeline for
// the GoNext media library.
//
// # What it does
//
// When an operator uploads a video file through the admin Media
// Library, the original bytes are a one-shot, single-bitrate,
// container-and-codec-of-the-uploader's-choosing blob. Serving that
// blob directly works for small clips but is a poor fit for the public
// web: every viewer pulls the entire file before they can seek to a
// timestamp, mobile networks can't keep up with the source bitrate,
// and the browser may not even support the upload's codec.
//
// videoproc closes that gap by transcoding the source into an HLS
// (HTTP Live Streaming) playlist — an index.m3u8 manifest pointing at
// 6-second .ts segments. The playlist is what the <video src=>
// attribute consumes; the segments are what the player streams as
// the user scrubs. HLS is supported natively by Safari and via
// hls.js / Media Source Extensions everywhere else.
//
// For v1 we ship a single 720p rendition (issue #52). A multi-bitrate
// ladder (240p / 480p / 720p / 1080p) is a follow-up; the Transcoder
// interface is shaped so the ladder lands as a config-driven variant
// list rather than a code change.
//
// # Why ffmpeg
//
// ffmpeg is the de-facto open-source video toolchain. We could shell
// to a managed transcoding service (AWS MediaConvert, Mux, etc.) but
// that would either pin operators to a single cloud or require a
// pluggable encoder layer this surface doesn't need yet. The price of
// shelling to ffmpeg is a runtime dependency on the host: the worker
// container has to ship the binary on PATH, and a deployment without
// it must degrade gracefully (the upload still succeeds, the row
// commits, and the player falls back to the original mp4).
//
// # Skip-graceful when ffmpeg is missing
//
// IsAvailable probes the PATH at worker boot. The worker's task
// registration consults this flag: if ffmpeg isn't present, the spec
// is registered with a stub handler that logs at warn and returns nil
// for every payload. Boot does NOT fail, because that would prevent
// the rest of the worker (email, webhooks, image processing) from
// running on a deployment that doesn't care about video.
//
// # Testability
//
// The package uses an injectable Runner interface for the actual
// ffmpeg invocation. Production wires it to the real exec.Command;
// tests substitute a recording fake that captures the arguments and
// fabricates the output directory. The test path never spawns a
// subprocess.
package videoproc
