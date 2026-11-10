package rootcause

import (
	"math"
	"sort"

	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/profile"
)

type rootGroup struct {
	root     flame.Frame
	targetID string
	samples  []flame.Sample
}
type fireGroup struct {
	from, to, last float64
	event          string
	samples        []flame.Sample
}
type rootEvent struct {
	from, to, maxEnd float64
	name             string
}

func (b builder) rootEvents() []rootEvent {
	var out []rootEvent
	seen := map[string]bool{}
	for _, r := range b.rec.Records {
		if r.Type != model.TypeScript || profile.IsGC(r) || profile.CountOnly(r.EventType) {
			continue
		}
		for _, part := range b.active(r.StartTime-b.rec.StartTime, r.EndTime-b.rec.StartTime) {
			name := eventName(r)
			key := name + "\x00" + decimal(part.Start) + "\x00" + decimal(part.End)
			if !seen[key] {
				seen[key] = true
				out = append(out, rootEvent{from: part.Start, to: part.End, name: name})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].from != out[j].from {
			return out[i].from < out[j].from
		}
		if out[i].to != out[j].to {
			return out[i].to < out[j].to
		}
		return out[i].name < out[j].name
	})
	for i := range out {
		out[i].maxEnd = out[i].to
		if i > 0 {
			out[i].maxEnd = math.Max(out[i].maxEnd, out[i-1].maxEnd)
		}
	}
	return out
}

func (b builder) rootFires(samples []flame.Sample, events []rootEvent) []fireGroup {
	var fires []*fireGroup
	byEvent := map[string]*fireGroup{}
	var burst *fireGroup
	for _, s := range samples {
		var chosen *rootEvent
		end := sort.Search(len(events), func(i int) bool { return events[i].from > s.Timestamp })
		for i := end - 1; i >= 0 && events[i].maxEnd > s.Timestamp; i-- {
			e := &events[i]
			if s.Timestamp < e.to && (chosen == nil || e.to-e.from < chosen.to-chosen.from || e.to-e.from == chosen.to-chosen.from && e.name < chosen.name) {
				chosen = e
			}
		}
		if chosen != nil {
			key := chosen.name + "\x00" + decimal(chosen.from) + "\x00" + decimal(chosen.to)
			f := byEvent[key]
			if f == nil {
				f = &fireGroup{from: chosen.from, to: chosen.to, event: chosen.name}
				byEvent[key] = f
				fires = append(fires, f)
			}
			f.samples = append(f.samples, s)
			f.last = s.Timestamp
			burst = nil
			continue
		}
		// A short timestamp gap identifies one sampled burst, never one fire per sample.
		if burst == nil || s.Timestamp-burst.last > .005000001 {
			burst = &fireGroup{from: s.Timestamp, to: s.Timestamp, event: "sampled stack burst"}
			fires = append(fires, burst)
		}
		burst.samples = append(burst.samples, s)
		burst.last = s.Timestamp
		burst.to = s.Timestamp
	}
	out := make([]fireGroup, 0, len(fires))
	for _, f := range fires {
		out = append(out, *f)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].from != out[j].from {
			return out[i].from < out[j].from
		}
		if out[i].to != out[j].to {
			return out[i].to < out[j].to
		}
		return out[i].event < out[j].event
	})
	return out
}

func (b builder) periodicRoots() []PeriodicRoot {
	groups := map[string]*rootGroup{}
	for _, s := range b.tree.Samples {
		if len(s.Frames) == 0 {
			continue
		}
		root := s.Frames[0]
		key := stackKey([]flame.Frame{root}) + ":" + digest(s.TargetID)
		g := groups[key]
		if g == nil {
			g = &rootGroup{root: root, targetID: s.TargetID}
			groups[key] = g
		}
		g.samples = append(g.samples, s)
	}
	events := b.rootEvents()
	var out []PeriodicRoot
	for key, g := range groups {
		fires := b.rootFires(g.samples, events)
		if len(fires) < 4 {
			continue
		}
		periods := make([]float64, 0, len(fires)-1)
		for i := 1; i < len(fires); i++ {
			periods = append(periods, (fires[i].from-fires[i-1].from)*1000)
		}
		sort.Float64s(periods)
		period := percentile(periods, .5)
		if period < 10 {
			continue
		}
		deviations := make([]float64, len(periods))
		var sumDeviation, maxDeviation float64
		for i, p := range periods {
			deviations[i] = math.Abs(p - period)
			sumDeviation += deviations[i]
			maxDeviation = math.Max(maxDeviation, deviations[i])
		}
		sort.Float64s(deviations)
		jitter := percentile(deviations, .95)
		regularity := math.Max(0, 1-sumDeviation/float64(len(periods))/period)
		if regularity < .9 || jitter/period > .15 || maxDeviation/period > .25 {
			continue
		}
		id := "periodic." + key
		p := PeriodicRoot{Root: g.root, TargetID: g.targetID, FireCount: len(fires), PeriodMs: number(period), JitterMs: number(jitter), Regularity: number(regularity), Command: b.query("paths "+quote(flame.FunctionID(g.root)), b.from, b.to)}
		for i, f := range fires {
			var ms float64
			for _, s := range f.samples {
				ms += s.WeightMs
			}
			p.TotalMs += ms
			fireID := id + ".fire." + decimal(float64(i))
			fire := Fire{FromSec: b.exportTime(f.from), ToSec: b.exportTime(f.to), SelfMs: number(ms), Samples: len(f.samples), Event: f.event, Command: b.query("stacks", f.from, math.Max(f.to, f.from+.000000001))}
			fire.Stats = []Stat{
				b.stat(fireID+".from_sec", "Fire start", fire.FromSec, "s", timelineBasis),
				b.stat(fireID+".to_sec", "Fire end", fire.ToSec, "s", timelineBasis),
				b.stat(fireID+".self_ms", "Estimated sampled cost per fire", ms, "ms", sampledBasis),
				b.stat(fireID+".samples", "Samples deduplicated into this fire", float64(fire.Samples), "count", sampledBasis),
			}
			p.Fires = append(p.Fires, fire)
		}
		p.TotalMs = number(p.TotalMs)
		p.MeanCostMs = number(p.TotalMs / float64(p.FireCount))
		p.Evidence = b.stacks(g.samples, id)
		p.Stats = []Stat{
			b.stat(id+".fires", "Deduplicated root fires", float64(p.FireCount), "count", "event intervals or timestamp-grouped stack bursts"),
			b.stat(id+".period_ms", "Median inter-fire period", p.PeriodMs, "ms", "deduplicated fire timestamps"),
			b.stat(id+".jitter_ms", "P95 absolute period deviation", p.JitterMs, "ms", "deduplicated fire timestamps"),
			b.stat(id+".regularity", "Period regularity", p.Regularity, "ratio", "1 - mean absolute period deviation / median period"),
			b.stat(id+".mean_cost_ms", "Mean sampled cost per fire", p.MeanCostMs, "ms", sampledBasis),
			b.stat(id+".total_ms", "Total sampled cost across fires", p.TotalMs, "ms", sampledBasis),
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TotalMs != out[j].TotalMs {
			return out[i].TotalMs > out[j].TotalMs
		}
		if flame.FunctionID(out[i].Root) != flame.FunctionID(out[j].Root) {
			return frameLess(out[i].Root, out[j].Root)
		}
		return out[i].Stats[0].Key < out[j].Stats[0].Key
	})
	return out
}
