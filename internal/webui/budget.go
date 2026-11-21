package webui

import (
	"fmt"
	"io"
)

const (
	maxArrayItems = 250_000
	maxJSONItems  = 1_000_000
	maxJSONDepth  = 128
)

type container struct {
	kind     byte
	nonempty bool
	items    int
}

// jsonBudget counts container entries before model.Parse allocates its typed
// slices. It does not validate JSON; malformed input is still rejected by the
// recording decoder. Count object members too, since malformed recordings can
// cause the decoder to inspect their top-level keys.
type jsonBudget struct {
	r               io.Reader
	stack           [maxJSONDepth]container
	depth, items    int
	quoted, escaped bool
	err             error
}

func (b *jsonBudget) Read(p []byte) (int, error) {
	if b.err != nil {
		return 0, b.err
	}
	n, err := b.r.Read(p)
	for _, c := range p[:n] {
		if b.quoted {
			switch {
			case b.escaped:
				b.escaped = false
			case c == '\\':
				b.escaped = true
			case c == '"':
				b.quoted = false
			}
			continue
		}
		switch c {
		case ' ', '\t', '\r', '\n':
			continue
		case ']', '}':
			if b.depth > 0 {
				b.depth--
			}
			continue
		}
		if b.depth > 0 {
			current := &b.stack[b.depth-1]
			if !current.nonempty || c == ',' {
				current.nonempty = true
				current.items++
				b.items++
				if current.kind == '[' && current.items > maxArrayItems {
					b.err = fmt.Errorf("recording exceeds the limit of %d items in one JSON array", maxArrayItems)
					return 0, b.err
				}
				if b.items > maxJSONItems {
					b.err = fmt.Errorf("recording exceeds the limit of %d total JSON entries", maxJSONItems)
					return 0, b.err
				}
			}
		}
		switch c {
		case '"':
			b.quoted = true
		case '[', '{':
			if b.depth == len(b.stack) {
				b.err = fmt.Errorf("recording exceeds the JSON nesting limit of %d levels", maxJSONDepth)
				return 0, b.err
			}
			b.stack[b.depth] = container{kind: c}
			b.depth++
		}
	}
	return n, err
}
