package surriti

import (
	"math"
	"testing"
)

func TestQualifierHashPythonCompatibility(t *testing.T) {
	cases := []struct {
		q    map[string]any
		want string
	}{
		{map[string]any{}, ""},
		{map[string]any{"season": "winter"}, "a4709ad6d8fc42b7"},
		{map[string]any{"b": 2, "a": 1}, "3d0089be7edf6746"},
		{map[string]any{"b": float64(2), "a": float64(1)}, "26b362b4c76c3011"},
		{map[string]any{"x": true, "n": 3.5}, "61ae34220082491a"},
		{map[string]any{"x": 1.0}, "f6933514092e0d6f"},
		{map[string]any{"x": math.Copysign(0, -1)}, "c9af70b819701295"},
		{map[string]any{"city": "München"}, "bf90d9950d863fca"},
		{map[string]any{"emoji": "😀"}, "f12ba041153d6e67"},
		{map[string]any{"nested": map[string]any{"z": 1.0, "a": []any{"é", true, nil, 2}}}, "829fababc92c8c87"},
		{map[string]any{"small": 1e-7, "large": 1e20}, "498c2ad78dbb545c"},
	}
	for _, tc := range cases {
		if got := QualifierHash(tc.q); got != tc.want {
			t.Fatalf("QualifierHash(%v)=%q want %q", tc.q, got, tc.want)
		}
	}
}

func TestDefaultFrameAliasResolution(t *testing.T) {
	r := NewRelationFrameRegistry(true, nil)
	f, ok := r.Get("wife_of", "")
	if !ok || f.CanonicalName != "spouse_of" {
		t.Fatalf("wife_of did not resolve to spouse_of: %#v %v", f, ok)
	}
}

func TestGroupFrameOverridesGlobal(t *testing.T) {
	r := NewRelationFrameRegistry(true, nil)
	custom := RelationFrame{CanonicalName: "custom_home", Aliases: []string{"lives_in"}}
	r.Register(custom, "g1")
	f, ok := r.Get("lives_in", "g1")
	if !ok || f.CanonicalName != "custom_home" {
		t.Fatalf("group override failed: %#v %v", f, ok)
	}
	global, ok := r.Get("lives_in", "g2")
	if !ok || global.CanonicalName != "lives_in" {
		t.Fatalf("global fallback changed: %#v %v", global, ok)
	}
}

func TestNormalizeSymmetric(t *testing.T) {
	a, b := NormalizeSymmetric("z", "a")
	if a != "a" || b != "z" {
		t.Fatalf("got %q,%q", a, b)
	}
}
