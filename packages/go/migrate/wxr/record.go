package wxr

// Record is the sum type emitted by Parser.Next. Each concrete type
// (*Author, *Category, *Tag, *Post) satisfies the interface via the
// unexported recordTag method, which prevents foreign packages from
// inadvertently inhabiting the sum.
//
// Callers branch with a type switch:
//
//	for {
//	    rec, err := p.Next()
//	    if errors.Is(err, io.EOF) {
//	        break
//	    }
//	    if err != nil {
//	        return err
//	    }
//	    switch r := rec.(type) {
//	    case *Author:   // ...
//	    case *Category: // ...
//	    case *Tag:      // ...
//	    case *Post:     // ...
//	    }
//	}
type Record interface {
	// Kind returns a stable short label ("author", "category", "tag",
	// "post") for logging and metrics. It exists in addition to the
	// type switch so importers can filter without reflect.
	Kind() string

	// recordTag is unexported so external types cannot inhabit Record.
	recordTag()
}

// Kind values are part of the public surface — they appear in
// importer logs and metrics. Keep them stable.
const (
	KindAuthor   = "author"
	KindCategory = "category"
	KindTag      = "tag"
	KindPost     = "post"
)

func (*Author) Kind() string   { return KindAuthor }
func (*Category) Kind() string { return KindCategory }
func (*Tag) Kind() string      { return KindTag }
func (*Post) Kind() string     { return KindPost }

func (*Author) recordTag()   {}
func (*Category) recordTag() {}
func (*Tag) recordTag()      {}
func (*Post) recordTag()     {}
