package wxr

import "errors"

// ErrMalformedXML is returned when the underlying xml.Decoder reports a
// syntax error mid-stream — truncated tag, mismatched closer, invalid
// entity, etc. Callers typically can't recover (the stream is in an
// undefined state), but the typed sentinel lets importers distinguish
// "bad input" from "I/O failure" when surfacing the error to the user.
var ErrMalformedXML = errors.New("wxr: malformed XML")

// ErrUnsupportedVersion is returned by Header when the <wp:wxr_version>
// declares anything other than 1.2 or 1.3. WordPress has shipped only
// these two formats for over a decade; older 1.0/1.1 files are rare and
// structurally different enough that we refuse rather than silently
// misparse them.
var ErrUnsupportedVersion = errors.New("wxr: unsupported WXR version")

// ErrHeaderConsumed is returned by Header on the second and subsequent
// calls. The parser is single-pass; the header is only available once.
var ErrHeaderConsumed = errors.New("wxr: header already consumed")

// ErrHeaderRequired is returned by Next when called before Header. The
// channel preamble must be parsed before items can be streamed.
var ErrHeaderRequired = errors.New("wxr: Header must be called before Next")
