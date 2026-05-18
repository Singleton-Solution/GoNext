package verify

import (
	"github.com/Singleton-Solution/GoNext/packages/go/migrate/wxr"
)

// makeComments builds a []wxr.Comment from a flat (id, parent)
// pairs slice. Used by unit tests of the source-side histogram
// without bringing the WXR parser into scope.
func makeComments(idParentPairs ...string) []wxr.Comment {
	if len(idParentPairs)%2 != 0 {
		panic("makeComments: odd-length args; pass (id, parent) pairs")
	}
	out := make([]wxr.Comment, 0, len(idParentPairs)/2)
	for i := 0; i < len(idParentPairs); i += 2 {
		out = append(out, wxr.Comment{
			ID:       idParentPairs[i],
			Parent:   idParentPairs[i+1],
			Content:  "body",
			Approved: "1",
		})
	}
	return out
}
