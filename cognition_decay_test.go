package surriti

import (
	"math"
	"testing"
	"time"
)

func TestEffectiveConfidenceConsolidatedNeverDecays(t *testing.T) {
	e := NewEntityEdge("a", "b", "x", "g")
	e.Confidence = .7
	e.Stability = "consolidated"
	e.CreatedAt = time.Now().Add(-10 * 365 * 24 * time.Hour)
	if got := EffectiveConfidence(e, time.Now().UTC(), nil); math.Abs(got-.7) > 1e-12 {
		t.Fatalf("got %v", got)
	}
}

func TestActivationPrefersRecent(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	old := NewEntityEdge("a", "b", "x", "g")
	old.Attributes = RecordActivationEvent(nil, now.Add(-30*24*time.Hour), 1, 8)
	recent := NewEntityEdge("a", "b", "x", "g")
	recent.Attributes = RecordActivationEvent(nil, now.Add(-time.Hour), 1, 8)
	if Activation(recent, now) <= Activation(old, now) {
		t.Fatal("recent presentation should activate more strongly")
	}
}

func TestRecallPresentationWeakerThanReassertion(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	weak := NewEntityEdge("a", "b", "x", "g")
	weak.Attributes = RecordActivationEvent(nil, now.Add(-time.Hour), ActivationRecallWeight, 8)
	strong := NewEntityEdge("a", "b", "x", "g")
	strong.Attributes = RecordActivationEvent(nil, now.Add(-time.Hour), ActivationReinforcementWeight, 8)
	if Activation(weak, now) >= Activation(strong, now) {
		t.Fatal("recall should be weaker")
	}
}

func TestRecordActivationCapsExactWindowButRetainsMass(t *testing.T) {
	attrs := map[string]any{}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 12; i++ {
		attrs = RecordActivationEvent(attrs, base.Add(time.Duration(i)*time.Hour), 1, 8)
	}
	events := asAnySlice(attrs[activationEventsKey])
	if len(events) != 8 {
		t.Fatalf("events=%d", len(events))
	}
	total, _ := toFloat(attrs[activationTotalWeightKey])
	if total != 12 {
		t.Fatalf("total=%v", total)
	}
}
