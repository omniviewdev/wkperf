package flame

//go:generate node gen_react_names.mjs

import (
	"cmp"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"slices"
	"strconv"
	"strings"

	"github.com/omniviewdev/wkperf/internal/model"
)

type frameKey struct {
	name, url, sourceID string
	line, column        int
}

func key(frame Frame) frameKey {
	sourceID := frame.SourceID
	if frame.URL != "" {
		sourceID = ""
	}
	return frameKey{frame.Name, frame.URL, sourceID, frame.Line, frame.Column}
}

// FunctionID depends on generated identity, not sample weight or source maps.
func FunctionID(frame Frame) string {
	k := key(frame)
	h := sha256.New()
	var size [8]byte
	for _, field := range []string{k.name, k.url, k.sourceID} {
		binary.BigEndian.PutUint64(size[:], uint64(len(field)))
		h.Write(size[:])
		h.Write([]byte(field))
	}
	binary.BigEndian.PutUint64(size[:], uint64(k.line))
	h.Write(size[:])
	binary.BigEndian.PutUint64(size[:], uint64(k.column))
	h.Write(size[:])
	return "f_" + hex.EncodeToString(h.Sum(nil))
}

func contextID(parentID, functionID string) string {
	h := sha256.New()
	h.Write([]byte(parentID))
	h.Write([]byte{0})
	h.Write([]byte(functionID))
	return "n_" + hex.EncodeToString(h.Sum(nil))
}

// StackID is the canonical calling-context ID of a root-to-leaf stack.
// It matches the path selectors returned by Tree.Paths.
func StackID(frames []Frame) string {
	id := "root"
	for _, frame := range frames {
		id = contextID(id, FunctionID(frame))
	}
	if len(frames) == 0 {
		return ""
	}
	return id
}

func compareFrame(a, b Frame) int {
	if c := cmp.Compare(a.Name, b.Name); c != 0 {
		return c
	}
	if c := cmp.Compare(a.URL, b.URL); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Line, b.Line); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Column, b.Column); c != 0 {
		return c
	}
	if c := cmp.Compare(a.SourceID, b.SourceID); c != 0 {
		return c
	}
	if a.Original == nil && b.Original != nil {
		return -1
	}
	if a.Original != nil && b.Original == nil {
		return 1
	}
	if a.Original != nil {
		if c := cmp.Compare(a.Original.Name, b.Original.Name); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Original.URL, b.Original.URL); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Original.Line, b.Original.Line); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Original.Column, b.Original.Column); c != 0 {
			return c
		}
	}
	return 0
}

func compareStack(a, b []Frame) int {
	for i := range min(len(a), len(b)) {
		if c := compareFrame(a[i], b[i]); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(a), len(b))
}

func fromModel(raw model.StackFrame, mapper FrameMapper, classifier *Classifier) Frame {
	frame := Frame{Name: raw.Name, URL: raw.URL, Line: raw.Line, Column: raw.Column,
		SourceID: raw.SourceID, Native: raw.URL == "", Anonymous: raw.Name == ""}
	if raw.Original != nil {
		frame.Original = &Location{Name: raw.Original.Name, URL: raw.Original.URL, Line: raw.Original.Line, Column: raw.Original.Column}
	}
	if mapper != nil {
		if original := mapper(raw); original != nil {
			copy := *original
			frame.Original = &copy
		}
	}
	frame.Category = classifier.Categorize(frame)
	frame.ID = FunctionID(frame)
	return frame
}

// Categorize applies the default ownership classifier. Use NewClassifier to
// reuse explicit URL rules without losing them during frame conversions.
func Categorize(frame Frame) Category {
	return defaultClassifier.Categorize(frame)
}

var defaultClassifier Classifier

func reactFunction(name string) bool {
	if _, found := slices.BinarySearch(react19FunctionNames[:], name); found {
		return true
	}
	// WebKit captures also expose these React 19 completion labels.
	if name == "completeRoot" || name == "completeRootWhenReady" {
		return true
	}
	return strings.HasPrefix(name, "commit") || strings.HasPrefix(name, "recursivelyTraverse") ||
		strings.HasSuffix(name, "OnFiber") || strings.HasPrefix(name, "reconcileChild") ||
		strings.HasPrefix(name, "bailoutOn") || strings.HasPrefix(name, "setValueFor") ||
		((strings.HasPrefix(name, "mount") || strings.HasPrefix(name, "update")) && strings.Contains(name, "Hook")) ||
		(strings.HasPrefix(name, "flush") && strings.HasSuffix(name, "Effects"))
}

// NearestNonFrameworkAncestor excludes node itself, the synthetic root, and
// framework, native, and GC frames. Unknown and injected ancestors are context,
// not evidence that application code scheduled the sampled work.
func NearestNonFrameworkAncestor(node *Node) *Node {
	if node == nil {
		return nil
	}
	for node = node.Parent; node != nil && node.Parent != nil; node = node.Parent {
		if node.Frame.Category == CategoryApp || node.Frame.Category == CategoryUnknownBundled || node.Frame.Category == CategoryInjected {
			return node
		}
	}
	return nil
}

// FrameLabel retains both mapped display and generated identity for folded data.
func FrameLabel(frame Frame) string {
	name := frame.Name
	if name == "" {
		name = "(anonymous)"
	}
	location := frame.URL
	if location == "" {
		location = "native"
		if frame.SourceID != "" {
			location += ":" + frame.SourceID
		}
	}
	generated := name + "@" + location + ":" + strconv.Itoa(frame.Line) + ":" + strconv.Itoa(frame.Column)
	if frame.Original == nil || frame.Original.URL == "" {
		return generated
	}
	if frame.Original.Name != "" {
		name = frame.Original.Name
	}
	return name + "@" + frame.Original.URL + ":" + strconv.Itoa(frame.Original.Line) + ":" + strconv.Itoa(frame.Original.Column) + " [generated " + generated + "]"
}
