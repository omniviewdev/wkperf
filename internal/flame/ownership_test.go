package flame

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestOwnershipUsesCapturedEvidence(t *testing.T) {
	bundle := "wails://localhost/assets/index-TEST.js"
	for _, tc := range []struct {
		name  string
		frame Frame
		want  Category
	}{
		{"unresolved library helper", Frame{Name: "getFiniteAnimations", URL: bundle}, Category("unknown-bundled")},
		{"unresolved scheduler", Frame{Name: "tick", URL: bundle}, Category("unknown-bundled")},
		{"unresolved closure", Frame{URL: bundle}, Category("unknown-bundled")},
		{"injected script", Frame{Name: "bridge", URL: "user-script:2"}, Category("injected")},
		{"first party plugin", Frame{Name: "ResourceTableContainer", URL: "wails://localhost/_/plugins/kubernetes/assets/entry2.js"}, CategoryApp},
		{"first party commit name", Frame{Name: "commitRows", URL: "wails://localhost/_/plugins/kubernetes/assets/entry2.js"}, CategoryApp},
		{"plugin name alone", Frame{Name: "run", URL: "/plugins/aws/plugin.js"}, Category("unknown-bundled")},
		{"source function exact name", Frame{Name: "commitRoot", URL: "https://app.test/src/update.ts"}, CategoryApp},
		{"source function prefix", Frame{Name: "commitRows", URL: "/src/update.js"}, CategoryApp},
		{"source function suffix", Frame{Name: "saveOnFiber", URL: "/src/update.tsx"}, CategoryApp},
		{"source file without src directory", Frame{Name: "commitRoot", URL: "app.ts"}, CategoryApp},
		{"mapped user code", Frame{Name: "commitRoot", URL: bundle, Original: &Location{URL: "webpack:///app/rows.js"}}, CategoryApp},
		{"mapped library", Frame{Name: "getFiniteAnimations", URL: bundle, Original: &Location{URL: "webpack:///./node_modules/@base-ui/react/utils/getFiniteAnimations.ts"}}, CategoryFramework},
		{"mapped library cannot masquerade as src", Frame{Name: "commitRows", URL: bundle, Original: &Location{URL: "/node_modules/example/src/index.ts"}}, CategoryFramework},
		{"native React-like name", Frame{Name: "commitRoot"}, CategoryNative},
		{"collection", Frame{Name: "Garbage Collection", URL: bundle}, CategoryGC},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Categorize(tc.frame); got != tc.want {
				t.Fatalf("Categorize(%+v) = %q, want %q", tc.frame, got, tc.want)
			}
		})
	}
}

func TestBundledReact19OwnershipNames(t *testing.T) {
	for _, name := range []string{
		"commitDeletionEffectsOnFiber", "commitHookEffectListMount", "commitHookEffectListUnmount",
		"commitPassiveUnmountOnFiber", "commitReconciliationEffects", "commitHostUpdate",
		"insertOrAppendPlacementNode", "setValueForStyle", "setValueForStyles", "setValueForAttribute",
		"setProp", "setInitialProperties", "updateProperties", "reconcileChildFibersImpl",
		"reconcileChildrenArray", "createChild", "updateSlot", "bailoutOnAlreadyFinishedWork",
		"propagateParentContextChanges", "updateSyncExternalStore", "mountWorkInProgressHook",
		"performWorkOnRootViaSchedulerTask", "dispatchEventForPluginEventSystem", "batchedUpdates$1", "jsxProd",
		"updateWorkInProgressHook", "pop", "pushHostContext", "FiberNode", "createFiberFromTypeAndProps",
		"flushMutationEffects", "flushLayoutEffects", "flushPendingEffects", "flushSpawnedWork",
		"completeRoot", "completeRootWhenReady", "recursivelyTraverseMutationEffects",
		"commitNewReactPhase", "reconcileChildFuture", "bailoutOnFutureWork", "setValueForFutureProp",
		"mountFutureHookState", "updateFutureHookState", "flushFutureEffects", "futureWorkOnFiber",
	} {
		t.Run(name, func(t *testing.T) {
			if got := Categorize(Frame{Name: name, URL: "wails://localhost/assets/index-TEST.js"}); got != CategoryFramework {
				t.Fatalf("bundled React %q = %q, want framework", name, got)
			}
			if got := Categorize(Frame{Name: name, URL: "/src/user-code.js"}); got != CategoryApp {
				t.Fatalf("source %q = %q, want app", name, got)
			}
		})
	}
}

func TestReact19TableUsesOnlyBundledNames(t *testing.T) {
	if !slices.IsSorted(react19FunctionNames[:]) {
		t.Fatal("React name table must retain binary-search order")
	}
	for i, name := range react19FunctionNames {
		if i > 0 && react19FunctionNames[i-1] == name {
			t.Fatalf("duplicate React name %q", name)
		}
		bundled := Frame{Name: name, URL: "/assets/index-bundled.js"}
		if got := Categorize(bundled); got != CategoryFramework {
			t.Fatalf("React name %q = %q in bundle", name, got)
		}
		bundled.Original = &Location{URL: "/workspace/user.js"}
		if got := Categorize(bundled); got != CategoryApp {
			t.Fatalf("mapped user function %q = %q", name, got)
		}
		if got := Categorize(Frame{Name: name, URL: "/src/user.js"}); got != CategoryApp {
			t.Fatalf("source user function %q = %q", name, got)
		}
	}
}

func TestOwnershipRuleValidationAndGlobSemantics(t *testing.T) {
	for _, pattern := range []string{"", " ", "[", "[]", "[!]", "[z-a]", "x]", "x[/]"} {
		if err := (OwnershipRules{AppURLs: []string{pattern}}).Validate(); err == nil {
			t.Errorf("invalid app glob %q accepted", pattern)
		}
		if err := (OwnershipRules{LibraryURLs: []string{pattern}}).Validate(); err == nil {
			t.Errorf("invalid library glob %q accepted", pattern)
		}
	}
	for _, tc := range []struct {
		pattern string
		url     string
		match   bool
	}{
		{"https://app.test/assets/*.js", "https://app.test/assets/app.js", true},
		{"https://app.test/assets/*.js", "https://app.test/assets/nested/app.js", false},
		{"https://app.test/assets/**.js", "https://app.test/assets/nested/app.js", true},
		{"**/assets/**/*.js", "https://app.test/assets/app.js", true},
		{"**/assets/**/*.js", "https://app.test/assets/deep/app.js", true},
		{"**/app?.[jt]s", "/app2.ts", true},
		{"**/app?.[jt]s", "/app22.ts", false},
		{"**/app[!0-9].js", "/appx.js", true},
		{"**/app[!0-9].js", "/app2.js", false},
		{"**/app[!0-9].js", "/app/.js", false},
		{"**/app[.-0].js", "/app/.js", false},
		{"**/app[.-0].js", "/app0.js", true},
		{"https://app.test/exact.js", "https://other.test/exact.js", false},
		{"**/App.js", "/app.js", false},
		{`C:\workspace\**\*.js`, `C:\workspace\deep\app.js`, true},
	} {
		rules := OwnershipRules{AppURLs: []string{tc.pattern}}
		if err := rules.Validate(); err != nil {
			t.Fatalf("valid glob %q rejected: %v", tc.pattern, err)
		}
		pattern, err := compileOwnershipGlob(tc.pattern)
		if err != nil {
			t.Fatal(err)
		}
		got := pattern.MatchString(strings.ReplaceAll(tc.url, "\\", "/"))
		if got && NewClassifier(rules).Categorize(Frame{Name: "opaqueUserCode", URL: tc.url}) != CategoryApp {
			t.Fatalf("matching URL %q lost explicit app override", tc.url)
		}
		if got != tc.match {
			t.Errorf("%q matching %q = %v, want %v", tc.pattern, tc.url, got, tc.match)
		}
	}
}

func TestOwnershipOverridesAreDeterministic(t *testing.T) {
	bundle := "wails://localhost/assets/index-TEST.js"
	rules := OwnershipRules{
		AppURLs:     []string{"**/assets/**", "**/node_modules/example/**"},
		LibraryURLs: []string{"**/_/plugins/third-party/**", "**/external/**"},
	}
	classifier := NewClassifier(rules)
	for _, tc := range []struct {
		frame Frame
		want  Category
	}{
		{Frame{Name: "commitRoot", URL: bundle}, CategoryApp},
		{Frame{Name: "getFiniteAnimations", URL: bundle}, CategoryApp},
		{Frame{Name: "cell", URL: "wails://localhost/_/plugins/third-party/assets/entry.js"}, CategoryFramework},
		{Frame{Name: "cell", URL: bundle, Original: &Location{URL: "/external/widget.js"}}, CategoryFramework},
		{Frame{Name: "commitRoot", URL: "/node_modules/example/index.js"}, CategoryApp},
		{Frame{Name: "bridge", URL: "user-script:2"}, CategoryInjected},
		{Frame{Name: "Garbage Collection", URL: bundle}, CategoryGC},
		{Frame{Name: "commitRoot"}, CategoryNative},
	} {
		if got := classifier.Categorize(tc.frame); got != tc.want {
			t.Errorf("%+v = %q, want %q", tc.frame, got, tc.want)
		}
	}
	overlap := OwnershipRules{AppURLs: []string{"**/assets/**"}, LibraryURLs: []string{"**/assets/**"}}
	if got := NewClassifier(overlap).Categorize(Frame{URL: bundle}); got != CategoryFramework {
		t.Fatalf("library override must win overlapping rules: %q", got)
	}
	for _, category := range []struct {
		frame Frame
		want  Category
	}{
		{Frame{Name: "bridge", URL: "user-script:2"}, CategoryInjected},
		{Frame{Name: "gc", URL: bundle}, CategoryGC},
		{Frame{Name: "native"}, CategoryNative},
	} {
		all := NewClassifier(OwnershipRules{AppURLs: []string{"**"}, LibraryURLs: []string{"**"}})
		if got := all.Categorize(category.frame); got != category.want {
			t.Errorf("intrinsic category overridden: %q, want %q", got, category.want)
		}
	}
}

func TestOwnershipSurvivesEveryTreeView(t *testing.T) {
	rec := testgen.ReactOwnership()
	rules := OwnershipRules{AppURLs: []string{"**/assets/index-*.js"}, LibraryURLs: []string{"**/_/plugins/**"}}
	tree := Build(rec, Options{Ownership: rules})
	originalRules := OwnershipRules{AppURLs: slices.Clone(rules.AppURLs), LibraryURLs: slices.Clone(rules.LibraryURLs)}
	plain := Build(rec)
	rules.AppURLs[0] = "changed"
	rules.LibraryURLs[0] = "changed"
	plugin := findFunction(t, tree, "cell")
	views := []*Tree{
		tree, tree.Inverted(), tree.Inverted().Canonical(), tree.Window(0, 5),
		tree.InIntervals([]Interval{{Start: 0, End: 5}}), tree.FocusFunction(plugin.ID),
		tree.FocusNode(tree.Root.Children[0].ID), tree.TimedOnly(),
	}
	for _, view := range views {
		if !reflect.DeepEqual(view.Ownership, originalRules) {
			t.Fatalf("view lost rule snapshot: %+v", view.Ownership)
		}
		view.EachSample(func(sample Sample) {
			for _, frame := range sample.Frames {
				if frame.Name == "cell" && frame.Category != CategoryFramework {
					t.Fatalf("plugin override lost in view: %+v", frame)
				}
				if frame.URL == "wails://localhost/assets/index-SYNTHETIC.js" && frame.Category != CategoryApp {
					t.Fatalf("app override lost in view: %+v", frame)
				}
			}
		})
	}
	for i, sample := range tree.Samples {
		if StackID(sample.Frames) != StackID(plain.Samples[i].Frames) {
			t.Fatal("ownership changed generated stack identity")
		}
		for j, frame := range sample.Frames {
			if frame.ID != plain.Samples[i].Frames[j].ID {
				t.Fatal("ownership changed generated function identity")
			}
		}
	}
	if got := Build(nil, Options{Ownership: originalRules}).Ownership; !reflect.DeepEqual(got, originalRules) {
		t.Fatalf("empty tree lost effective rules: %+v", got)
	}
}

func TestBundledReactCommitHasNoCapturedUserAncestor(t *testing.T) {
	tree := Build(testgen.ReactOwnership())
	near(t, tree.Root.TotalMs, 30)
	commit := tree.Samples[0]
	for _, frame := range commit.Frames[:len(commit.Frames)-1] {
		if frame.Category != CategoryFramework {
			t.Fatalf("React commit frame is not framework: %+v", frame)
		}
	}
	leaf := tree.byID[StackID(commit.Frames)]
	if got := NearestNonFrameworkAncestor(leaf); got != nil {
		t.Fatalf("framework-only stack fabricated scheduling context: %+v", got.Frame)
	}
	for _, sample := range tree.Samples {
		if len(sample.Frames) == 0 || sample.Frames[len(sample.Frames)-1].Name != "getAnimations" {
			continue
		}
		ancestor := NearestNonFrameworkAncestor(tree.byID[StackID(sample.Frames)])
		if ancestor == nil || ancestor.Frame.Name != "getFiniteAnimations" || ancestor.Frame.Category != CategoryUnknownBundled {
			t.Fatalf("unresolved library context claimed app ownership: %+v", ancestor)
		}
	}
	for _, category := range []Category{CategoryApp, CategoryUnknownBundled, CategoryInjected} {
		root := &Node{}
		context := &Node{Frame: Frame{Category: category}, Parent: root}
		native := &Node{Frame: Frame{Category: CategoryNative}, Parent: context}
		framework := &Node{Frame: Frame{Category: CategoryFramework}, Parent: native}
		leaf := &Node{Frame: Frame{Category: CategoryGC}, Parent: framework}
		if got := NearestNonFrameworkAncestor(leaf); got != context {
			t.Fatalf("nearest %q context = %+v, want context", category, got)
		}
	}
	if NearestNonFrameworkAncestor(nil) != nil {
		t.Fatal("nil node has an ancestor")
	}
}

func TestMappedOwnershipKeepsGeneratedIdentity(t *testing.T) {
	raw := model.StackFrame{Name: "commitRoot", URL: "/assets/index.js", Line: 4, Column: 2}
	rec := recording([][]model.StackFrame{{raw}}, []float64{.001})
	unmapped := Build(rec)
	for _, source := range []string{"/src/commit.js", "/node_modules/react-dom/commit.js"} {
		mapped := Build(rec, Options{Mapper: func(model.StackFrame) *Location {
			return &Location{Name: "commitRoot", URL: source, Line: 8, Column: 3}
		}})
		want := CategoryApp
		if source == "/node_modules/react-dom/commit.js" {
			want = CategoryFramework
		}
		if got := mapped.Functions()[0].Frame.Category; got != want {
			t.Fatalf("mapped %q = %q, want %q", source, got, want)
		}
		if mapped.Functions()[0].ID != unmapped.Functions()[0].ID || mapped.SamplePathIDs()[0] != unmapped.SamplePathIDs()[0] {
			t.Fatal("source mapping changed generated identity")
		}
	}
}
