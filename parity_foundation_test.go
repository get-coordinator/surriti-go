package surriti

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
)

func TestDriverQueryRequiresExplicitConnect(t *testing.T) {
	client := &fakeClient{}
	factory := &fakeFactory{openFn: func(context.Context, string, int) (DBClient, error) { return client, nil }}
	d := testDriver(t, factory)
	if _, err := d.Query(context.Background(), "SELECT 1", nil); !errors.Is(err, ErrConnection) {
		t.Fatalf("err=%v want ErrConnection", err)
	}
	if factory.count() != 0 {
		t.Fatalf("query auto-connected: opens=%d", factory.count())
	}
}

func TestDriverCloseSuppressesTransportFailureLikePython(t *testing.T) {
	client := &fakeClient{closeFn: func(context.Context) error { return errors.New("close failed") }}
	factory := &fakeFactory{openFn: func(context.Context, string, int) (DBClient, error) { return client, nil }}
	d := testDriver(t, factory)
	if err := d.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(context.Background()); err != nil {
		t.Fatalf("close leaked transport error: %v", err)
	}
}

func TestPropertyFilterZeroOperatorDefaultsToEquality(t *testing.T) {
	row := map[string]any{"kind": "person"}
	f := &SearchFilters{PropertyFilters: []PropertyFilter{{Name: "kind", Value: "person"}}}
	if !NodePassesFilters(row, f) {
		t.Fatal("zero-value PropertyFilter.Op must mean equality")
	}
	f.PropertyFilters[0].Value = "company"
	if NodePassesFilters(row, f) {
		t.Fatal("default equality admitted unequal value")
	}
}

func TestPropertyFilterUnknownOperatorIsNoopLikePython(t *testing.T) {
	row := map[string]any{"kind": "person"}
	f := &SearchFilters{PropertyFilters: []PropertyFilter{{Name: "kind", Value: "company", Op: ComparisonOperator("UNKNOWN")}}}
	if !NodePassesFilters(row, f) {
		t.Fatal("Python treats unknown comparison operators as an unconstrained predicate")
	}
}

func TestEmbedderCosineAllowsUnequalLengthsButRecallDoesNot(t *testing.T) {
	got := CosineSimilarity([]float64{1, 0}, []float64{1})
	if math.Abs(got-1) > 1e-12 {
		t.Fatalf("embedder cosine=%v want 1", got)
	}
	if got := MemoryCosineSimilarity([]float64{1, 0}, []float64{1}); got != 0 {
		t.Fatalf("memory cosine=%v want 0", got)
	}
}

func TestDummyLLMExtractionParity(t *testing.T) {
	llm := DummyLLMClient{}
	res, err := llm.Extract(context.Background(), ExtractionRequest{Content: "Michael works at Acme Corp. Alice met Michael;"})
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, e := range res.Entities {
		names = append(names, e.Name)
	}
	joined := strings.Join(names, "|")
	if !strings.Contains(joined, "Michael") || !strings.Contains(joined, "Acme Corp") || !strings.Contains(joined, "Alice") {
		t.Fatalf("entities=%v", names)
	}
	if len(res.Facts) == 0 {
		t.Fatal("dummy extractor produced no facts")
	}
}

func TestDummyLLMStructuredContradictionParity(t *testing.T) {
	f := NewExtractedFact("Alice", "works_at", "Globex")
	got := DummyFindContradictions(ContradictionRequest{
		NewFact:       "Alice no longer works at Acme; she moved to Globex.",
		ExistingFacts: []string{"Alice works at Acme."},
		Candidates:    []ContradictionCandidate{{Subject: "Alice", Object: "Acme", Fact: "Alice works at Acme."}},
		NewFactStruct: &f,
	})
	if len(got) != 1 || got[0] != 0 {
		t.Fatalf("got=%v want [0]", got)
	}
}

func TestScriptedLLMQueueAndContradictions(t *testing.T) {
	resp := ScriptedResponse{
		Entities:       []ExtractedEntity{NewExtractedEntity("Alice")},
		Facts:          []ExtractedFact{NewExtractedFact("Alice", "works_at", "Acme")},
		Contradictions: []int{0},
	}
	llm := NewScriptedLLMClient([]ScriptedResponse{resp})
	res, err := llm.Extract(context.Background(), ExtractionRequest{Content: "x", GroupID: "g"})
	if err != nil || len(res.Entities) != 1 {
		t.Fatalf("res=%v err=%v", res, err)
	}
	got, err := llm.FindContradictions(context.Background(), ContradictionRequest{NewFact: "y", ExistingFacts: []string{"x"}})
	if err != nil || len(got) != 1 || got[0] != 0 {
		t.Fatalf("got=%v err=%v", got, err)
	}
	empty, err := llm.Extract(context.Background(), ExtractionRequest{Content: "z"})
	if err != nil || len(empty.Entities) != 0 || len(empty.Facts) != 0 {
		t.Fatalf("exhausted response=%v err=%v", empty, err)
	}
}

func TestRRFParity(t *testing.T) {
	got := RRF([][]string{{"a", "b"}, {"b", "a"}}, 60)
	want := 1.0/61.0 + 1.0/62.0
	if math.Abs(got["a"]-want) > 1e-12 || math.Abs(got["b"]-want) > 1e-12 {
		t.Fatalf("scores=%v", got)
	}
}

func TestEpisodeMentionsRerankParity(t *testing.T) {
	rows := []map[string]any{
		{"uuid": "one", "episodes": []string{"a"}},
		{"uuid": "three", "episodes": []string{"a", "b", "c"}},
		{"uuid": "two", "episodes": []string{"a", "b"}},
	}
	got := EpisodeMentionsRerank(rows, 2)
	if asString(got[0]["uuid"]) != "three" || asString(got[1]["uuid"]) != "two" {
		t.Fatalf("order=%v", got)
	}
}

type retrievalParityDriver struct {
	updated []string
}

func (d *retrievalParityDriver) Query(_ context.Context, q string, vars map[string]any) (any, error) {
	if strings.Contains(q, `status = "silent"`) && strings.Contains(q, "SELECT") {
		return []map[string]any{{
			"uuid": "sleeping", "group_id": "g", "status": "silent",
			"fact": "staging deploy requires pgsslmode", "fact_embedding": []float64{1, 0},
			"in": "entity:a", "out": "entity:b", "attributes": map[string]any{},
		}}, nil
	}
	if strings.Contains(q, "UPDATE relates_to") {
		d.updated = append(d.updated, stringFromAny(vars["uuid"]))
	}
	return []map[string]any{}, nil
}

func TestStrongCueResurrectsSilentMemory(t *testing.T) {
	d := &retrievalParityDriver{}
	g := "g"
	row := ResurrectSilentMemory(context.Background(), d, []float64{1, 0}, &g, .8, nil, nil)
	if row == nil || asString(row["status"]) != "active" || row["_memory_resurrected"] != true {
		t.Fatalf("row=%v", row)
	}
	if len(d.updated) != 1 || d.updated[0] != "sleeping" {
		t.Fatalf("updated=%v", d.updated)
	}
}

func TestWeakCueDoesNotResurrectSilentMemory(t *testing.T) {
	d := &retrievalParityDriver{}
	g := "g"
	if row := ResurrectSilentMemory(context.Background(), d, []float64{0, 1}, &g, .8, nil, nil); row != nil {
		t.Fatalf("row=%v", row)
	}
	if len(d.updated) != 0 {
		t.Fatalf("updated=%v", d.updated)
	}
}
