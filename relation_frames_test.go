package surriti

import "testing"

func TestQualifierHashPythonCompatibility(t *testing.T) {
	cases := []struct {
		q    map[string]any
		want string
	}{
		{map[string]any{}, ""},
		{map[string]any{"season": "winter"}, "a4709ad6d8fc42b7"},
		{map[string]any{"b": float64(2), "a": float64(1)}, "3d0089be7edf6746"},
		{map[string]any{"x": true, "n": 3.5}, "61ae34220082491a"},
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
