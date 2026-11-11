package compare

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"
)

func TestCountsJSONUsesCamelCaseAndKeepsZeroes(t *testing.T) {
	data, err := json.Marshal(Counts{Improved: 2, Regressed: 1})
	if err != nil {
		t.Fatal(err)
	}
	var counts map[string]int
	if err := json.Unmarshal(data, &counts); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if !reflect.DeepEqual(keys, []string{"improved", "noisy", "regressed", "unchanged"}) {
		t.Fatalf("count keys = %v", keys)
	}
	if counts["improved"] != 2 || counts["regressed"] != 1 || counts["unchanged"] != 0 || counts["noisy"] != 0 {
		t.Fatalf("count values changed: %s", data)
	}
}
