package surriti

import (
	"math"
	"strings"
	"time"
)

var defaultHalfLifeDays = map[string]float64{
	"episodic": 30,
	"reinforced": 90,
	"persistent": 365,
	"consolidated": math.Inf(1),
}

const (
	DefaultDecayPointsPerDay = 0.01
	DefaultRecallBoost = 0.04
	DefaultReinforcementBoost = 0.03
	MaxRecallBoost = 0.2
	MaxReinforcementBoost = 0.25
	ActivationDecayD = 0.5
	ActivationRecallWeight = 0.15
	ActivationReinforcementWeight = 1.0
	ActivationMaxExactEvents = 8
	ActivationSilenceLine = -3.5
	ActivationScale = 0.8
)

const (
	activationEventsKey = "activation_events"
	activationTotalWeightKey = "activation_total_weight"
	activationCreatedKey = "activation_created_at"
)

var ProtectedMemoryClasses = map[string]struct{}{
	"constraint": {}, "style": {}, "preference": {}, "goal": {}, "trait": {}, "self_model": {}, "consolidated": {}, "archived_summary": {},
}

func HalfLifeFor(stability string, overrides map[string]float64) float64 {
	key := stability
	if key == "" {
		key = "episodic"
	}
	if overrides != nil {
		if v, ok := overrides[key]; ok {
			return v
		}
	}
	if v, ok := defaultHalfLifeDays[key]; ok {
		return v
	}
	return defaultHalfLifeDays["episodic"]
}

func IsDecayProtected(edge EntityEdge) bool {
	mc := strings.ToLower(strings.TrimSpace(edge.MemoryClass))
	if mc == "" {
		mc = "objective"
	}
	_, ok := ProtectedMemoryClasses[mc]
	return ok || edge.Stability == "consolidated"
}

func LinearVitality(edge EntityEdge, now time.Time, pointsPerDay float64) float64 {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	score := edge.DecayScore
	if IsDecayProtected(edge) {
		return clamp01(score)
	}
	var last *time.Time
	if edge.LastRecalledAt != nil {
		last = edge.LastRecalledAt
	} else if edge.LastReinforcedAt != nil {
		last = edge.LastReinforcedAt
	} else if edge.ValidAt != nil {
		last = edge.ValidAt
	} else {
		t := edge.CreatedAt
		last = &t
	}
	if last == nil {
		return clamp01(score)
	}
	days := math.Floor(math.Max(0, now.Sub(last.UTC()).Hours()/24))
	return clamp01(score - days*math.Max(0, pointsPerDay))
}

func pythonISO(t time.Time) string {
	t = t.UTC().Truncate(time.Microsecond)
	if t.Nanosecond() == 0 {
		return t.Format("2006-01-02T15:04:05+00:00")
	}
	return t.Format("2006-01-02T15:04:05.000000+00:00")
}

func cloneMap(src map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range src {
		out[k] = v
	}
	return out
}

func RecordActivationEvent(attributes map[string]any, when time.Time, weight float64, maxExact int) map[string]any {
	attrs := cloneMap(attributes)
	if when.IsZero() {
		when = time.Now().UTC()
	}
	w := math.Max(0, weight)
	events := asAnySlice(attrs[activationEventsKey])
	events = append(events, []any{pythonISO(when), w})
	if maxExact > 0 && len(events) > maxExact {
		events = events[len(events)-maxExact:]
	} else if maxExact <= 0 {
		events = []any{}
	}
	prior, _ := toFloat(attrs[activationTotalWeightKey])
	attrs[activationTotalWeightKey] = math.Max(0, prior) + w
	attrs[activationEventsKey] = events
	if _, ok := attrs[activationCreatedKey]; !ok {
		attrs[activationCreatedKey] = pythonISO(when)
	}
	return attrs
}

func asAnySlice(v any) []any {
	switch x := v.(type) {
	case []any:
		return append([]any{}, x...)
	case nil:
		return []any{}
	default:
		return []any{}
	}
}

func parseActivationTime(v any) (time.Time, bool) {
	switch x := v.(type) {
	case time.Time:
		return x.UTC(), true
	case *time.Time:
		if x != nil {
			return x.UTC(), true
		}
	case string:
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999-07:00", "2006-01-02 15:04:05-07:00"} {
			if t, err := time.Parse(layout, x); err == nil {
				return t.UTC(), true
			}
		}
	case float64:
		return time.Unix(int64(x), int64((x-math.Floor(x))*1e9)).UTC(), true
	case int64:
		return time.Unix(x, 0).UTC(), true
	case int:
		return time.Unix(int64(x), 0).UTC(), true
	}
	return time.Time{}, false
}

type activationEvent struct{ ts, weight float64 }

func activationState(edge EntityEdge) ([]activationEvent, float64, *float64) {
	attrs := edge.Attributes
	if attrs == nil {
		attrs = map[string]any{}
	}
	raw := asAnySlice(attrs[activationEventsKey])
	if len(raw) > ActivationMaxExactEvents {
		raw = raw[len(raw)-ActivationMaxExactEvents:]
	}
	events := make([]activationEvent, 0, len(raw))
	for _, item := range raw {
		var tv, wv any
		switch x := item.(type) {
		case map[string]any:
			tv = x["ts"]
			if tv == nil { tv = x["time"] }
			if tv == nil { tv = x["at"] }
			wv = x["weight"]
			if wv == nil { wv = 1.0 }
		case []any:
			if len(x) >= 2 {
				tv = x[0]
				wv = x[1]
			}
		default:
			continue
		}
		t, ok := parseActivationTime(tv)
		if !ok {
			continue
		}
		w, ok := toFloat(wv)
		if !ok {
			continue
		}
		events = append(events, activationEvent{float64(t.UnixNano()) / 1e9, math.Max(0, w)})
	}

	var created *float64
	if t, ok := parseActivationTime(attrs[activationCreatedKey]); ok {
		v := float64(t.UnixNano()) / 1e9
		created = &v
	} else {
		var t time.Time
		if edge.ValidAt != nil {
			t = *edge.ValidAt
		} else if !edge.CreatedAt.IsZero() {
			t = edge.CreatedAt
		} else if edge.LastReinforcedAt != nil {
			t = *edge.LastReinforcedAt
		}
		if !t.IsZero() {
			v := float64(t.UnixNano()) / 1e9
			created = &v
		}
	}

	if len(events) > 0 {
		total, _ := toFloat(attrs[activationTotalWeightKey])
		sum := 0.0
		for _, e := range events { sum += e.weight }
		if total < sum { total = sum }
		return events, total, created
	}

	var reinforced time.Time
	if edge.LastReinforcedAt != nil {
		reinforced = *edge.LastReinforcedAt
	} else if edge.ValidAt != nil {
		reinforced = *edge.ValidAt
	} else {
		reinforced = edge.CreatedAt
	}
	if !reinforced.IsZero() {
		count := edge.ReinforcementCount
		if count < 1 { count = 1 }
		events = append(events, activationEvent{float64(reinforced.UnixNano()) / 1e9, float64(count) * ActivationReinforcementWeight})
	}
	if edge.LastRecalledAt != nil && edge.RecallCount > 0 {
		events = append(events, activationEvent{float64(edge.LastRecalledAt.UnixNano()) / 1e9, float64(edge.RecallCount) * ActivationRecallWeight})
	}
	total := 0.0
	for _, e := range events { total += e.weight }
	if len(events) > ActivationMaxExactEvents {
		events = events[len(events)-ActivationMaxExactEvents:]
	}
	return events, total, created
}

func ActrBaseLevel(raw [][]float64, totalWeight *float64, createdTS *float64, nowTS *float64, d float64) float64 {
	events := make([]activationEvent, 0, len(raw))
	for _, e := range raw {
		if len(e) >= 2 {
			events = append(events, activationEvent{e[0], e[1]})
		}
	}
	return actrBaseLevelEvents(events, totalWeight, createdTS, nowTS, d)
}

func actrBaseLevelEvents(events []activationEvent, totalWeight *float64, createdTS *float64, nowTS *float64, d float64) float64 {
	now := float64(time.Now().UTC().UnixNano()) / 1e9
	if nowTS != nil { now = *nowTS }
	sortActivation(events)
	if len(events) > ActivationMaxExactEvents { events = events[len(events)-ActivationMaxExactEvents:] }
	s := 0.0
	exact := 0.0
	for _, e := range events {
		dt := math.Max((now-e.ts)/3600, .01)
		w := math.Max(0, e.weight)
		s += w * math.Pow(dt, -d)
		exact += w
	}
	total := exact
	if totalWeight != nil { total = *totalWeight }
	historical := math.Max(total-exact, 0)
	if historical > 0 && createdTS != nil {
		tlife := math.Max((now-*createdTS)/3600, .01)
		told := .01
		if len(events) > 0 { told = math.Max((now-events[0].ts)/3600, .01) }
		if tlife > told+1e-9 && d != 1 {
			s += historical * ((math.Pow(tlife, 1-d) - math.Pow(told, 1-d)) / ((1-d) * (tlife - told)))
		} else {
			s += historical * math.Pow(tlife, -d)
		}
	}
	if s > 0 { return math.Log(s) }
	return -10
}

func sortActivation(events []activationEvent) {
	for i := 1; i < len(events); i++ {
		for j := i; j > 0 && events[j].ts < events[j-1].ts; j-- {
			events[j], events[j-1] = events[j-1], events[j]
		}
	}
}

func Activation(edge EntityEdge, now time.Time) float64 {
	if now.IsZero() { now = time.Now().UTC() }
	events, total, created := activationState(edge)
	raw := make([][]float64, len(events))
	for i, e := range events { raw[i] = []float64{e.ts, e.weight} }
	n := float64(now.UnixNano()) / 1e9
	return ActrBaseLevel(raw, &total, created, &n, ActivationDecayD)
}

func ActivationVitality(edge EntityEdge, now time.Time) float64 {
	if IsDecayProtected(edge) { return 1 }
	b := Activation(edge, now)
	x := (b - ActivationSilenceLine) / ActivationScale
	if x <= -30 { return 0 }
	if x >= 30 { return 1 }
	return 1 / (1 + math.Exp(-x))
}

func EffectiveConfidence(edge EntityEdge, now time.Time, halfLifeOverrides map[string]float64) float64 {
	_ = halfLifeOverrides
	base := clamp01(edge.Confidence)
	if IsDecayProtected(edge) { return base }
	return clamp01(base * ActivationVitality(edge, now))
}

func clamp01(v float64) float64 {
	if v < 0 { return 0 }
	if v > 1 { return 1 }
	return v
}
