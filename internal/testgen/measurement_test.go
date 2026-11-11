package testgen

import (
	"testing"

	"github.com/omniviewdev/wkperf/internal/model"
)

func TestMeasurementFixturesUseWebKitScriptVocabulary(t *testing.T) {
	allowed := map[string]bool{
		"event-dispatched":      true,
		"timer-fired":           true,
		"animation-frame-fired": true,
		"microtask-dispatched":  true,
		"observer-callback":     true,
		"garbage-collected":     true,
	}
	for _, rec := range []*model.Recording{NestedRecords(), InputStallFrames(), GCOverlay(), ThresholdFrames(), ExclusiveFindingCosts(), GetAnimations(), React(), Periodic()} {
		for _, r := range rec.Records {
			if r.Type == model.TypeScript && !allowed[r.EventType] {
				t.Errorf("%s uses an event absent from WebKit vocabulary: %q", rec.DisplayName, r.EventType)
			}
		}
	}
}
