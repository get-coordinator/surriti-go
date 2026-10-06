package surriti

import "testing"

func TestCueTokensParity(t *testing.T) {
	got := CueTokens("please tell me about peanuts peanuts allergy", 16)
	want := []string{"peanuts", "allergy"}
	if !equalStrings(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	got = CueTokens("hi OK uh um AI, Go, UK, R, and US.", 16)
	want = []string{"ai", "go", "uk", "r", "us"}
	if !equalStrings(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestAdmissionParity(t *testing.T) {
	rows := []map[string]any{{"uuid": "irrelevant", "fact": "Mira likes watercolor painting", "name": "likes", "fact_embedding": []float64{0, 1}}}
	if got := AdmitCandidates(rows, "production database timeout", []float64{1, 0}, .25, 2); len(got) != 0 {
		t.Fatalf("unexpected %v", got)
	}
	semantic := map[string]any{"uuid": "semantic", "fact": "unrelated lexical surface", "name": "x", "fact_embedding": []float64{1, 0}}
	literal := map[string]any{"uuid": "literal", "fact": "production database uses pgsslmode require", "name": "config", "fact_embedding": []float64{0, 1}}
	got := AdmitCandidates([]map[string]any{semantic, literal}, "production database pgsslmode", []float64{1, 0}, .8, 2)
	if len(got) != 2 || got[0]["uuid"] != "semantic" || got[1]["uuid"] != "literal" {
		t.Fatalf("got %v", got)
	}
}

func TestSpreadingActivationParity(t *testing.T) {
	rows := []map[string]any{
		{"uuid": "seed", "in": "entity:a", "out": "entity:b", "episodes": []string{"ep1"}},
		{"uuid": "neighbor", "in": "entity:b", "out": "entity:c", "episodes": []string{"ep2"}},
		{"uuid": "remote", "in": "entity:x", "out": "entity:y", "episodes": []string{"ep9"}},
	}
	s := map[string]float64{"seed": 1, "neighbor": .4, "remote": .4}
	ApplySpreadingActivation(rows, s, .2, 1)
	if s["neighbor"] <= s["remote"] {
		t.Fatalf("scores %v", s)
	}
}

func TestEvidenceSnippetsParity(t *testing.T) {
	text := "We talked about lunch. The staging deploy fails with ECONNRESET unless PGSSLMODE=require is set. Then we discussed music."
	got := EvidenceSnippets(text, "staging ECONNRESET PGSSLMODE", 1, 240)
	if len(got) != 1 || !contains(got[0], "ECONNRESET") || !contains(got[0], "PGSSLMODE=require") {
		t.Fatalf("got %v", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
