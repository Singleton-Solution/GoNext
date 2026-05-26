package jobs

import (
	"encoding/json"
	"io"
)

// writeJSON renders v as indented JSON. The CLI uses indented output
// for human readability; piping through `jq .[]` flattens further
// when needed.
func writeJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
