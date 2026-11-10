package flame

import (
	"cmp"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
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

func fromModel(raw model.StackFrame, mapper FrameMapper) Frame {
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
	frame.Category = Categorize(frame)
	return frame
}

// Categorize uses original paths when available, then generated metadata.
func Categorize(frame Frame) Category {
	name, path := frame.Name, frame.URL
	if frame.Original != nil {
		if frame.Original.Name != "" {
			name = frame.Original.Name
		}
		if frame.Original.URL != "" {
			path = frame.Original.URL
		}
	}
	lower := strings.ToLower(name)
	if lower == "gc" || lower == "(gc)" || strings.Contains(lower, "garbage collect") || strings.HasPrefix(lower, "jsc::heap::collect") {
		return CategoryGC
	}
	if reactFunction(name) || reactFunction(frame.Name) {
		return CategoryFramework
	}
	path = strings.ToLower(strings.ReplaceAll(path, "\\", "/"))
	if strings.Contains(path, "node_modules/") || strings.Contains(path, "/vendor/") ||
		strings.Contains(path, "/vendor-") || strings.Contains(path, "/vendor.") ||
		strings.HasPrefix(path, "vendor/") || strings.HasPrefix(path, "vendor-") ||
		strings.Contains(path, "/plugins/") || strings.Contains(path, "/plugin/") ||
		strings.Contains(path, "/plugin-") || strings.Contains(path, "/plugin.") ||
		strings.HasPrefix(path, "plugin-") || strings.HasPrefix(path, "plugin.") ||
		strings.HasPrefix(path, "plugin://") || strings.HasPrefix(path, "plugins/") {
		return CategoryFramework
	}
	if path == "" {
		return CategoryNative
	}
	return CategoryApp
}

func reactFunction(name string) bool {
	switch name {
	case "performSyncWorkOnRoot", "performConcurrentWorkOnRoot", "performWorkOnRoot", "renderRootSync", "renderRootConcurrent",
		"workLoopSync", "workLoopConcurrent", "performUnitOfWork", "completeUnitOfWork", "beginWork", "completeWork",
		"renderWithHooks", "renderWithHooksAgain", "updateFunctionComponent", "updateClassComponent", "updateHostComponent",
		"flushSync", "flushSyncFromReconciler", "flushSyncWorkAcrossRoots", "flushSyncWorkAcrossRoots_impl", "flushSyncWorkOnAllRoots", "flushPassiveEffects",
		"flushPassiveEffectsImpl", "processRootScheduleInMicrotask", "performWorkUntilDeadline", "commitRoot", "commitRootImpl",
		"commitMutationEffects", "commitLayoutEffects", "commitPassiveMountEffects", "commitPassiveUnmountEffects",
		"dispatchAction", "dispatchSetState", "scheduleUpdateOnFiber", "ensureRootIsScheduled", "scheduleCallback":
		return true
	}
	return strings.HasPrefix(name, "commitOnFiber") || strings.HasPrefix(name, "commitMutationEffectsOnFiber") ||
		strings.HasPrefix(name, "commitLayoutEffectOnFiber") || strings.HasPrefix(name, "commitPassiveMountOnFiber") ||
		(strings.HasPrefix(name, "recursivelyTraverse") && strings.Contains(name, "Effects"))
}

// NearestAppAncestor excludes node itself and the synthetic root.
func NearestAppAncestor(node *Node) *Node {
	if node == nil {
		return nil
	}
	for node = node.Parent; node != nil && node.Parent != nil; node = node.Parent {
		if node.Frame.Category == CategoryApp {
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
