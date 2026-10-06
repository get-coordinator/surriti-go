package surriti

import (
	"context"
	"os"
	"strings"
	"testing"
)

type queryFunc func(context.Context, string, map[string]any) (any, error)

func (f queryFunc) Query(ctx context.Context, q string, vars map[string]any) (any, error) {
	return f(ctx, q, vars)
}

func TestQualifierHashPythonGolden(t *testing.T) {
	b, err := os.ReadFile("testdata/qualifier_hashes.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Value map[string]any
		Hash  string
	}
	if err := decodeJSONNumbers(b, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if got := QualifierHash(c.Value); got != c.Hash {
			t.Errorf("%v: hash=%s want %s", c.Value, got, c.Hash)
		}
	}
}

func TestProductionPromptContracts(t *testing.T) {
	for file, got := range map[string]string{"extraction_system": ExtractionSystemPrompt, "contradiction_system": ContradictionSystemPrompt, "frame_classification_system": FrameClassificationSystemPrompt} {
		want, err := os.ReadFile("testdata/" + file + ".txt")
		if err != nil {
			t.Fatal(err)
		}
		if got != string(want) {
			t.Errorf("%s differs from frozen Python prompt", file)
		}
	}
}

func TestExtractionPreservesQualifierNumbersAndInlineFences(t *testing.T) {
	result, err := parseExtractionJSON("```json{\"facts\":[{\"subject\":\"A\",\"object\":\"B\",\"qualifiers\":{\"n\":9007199254740993}}]} ```")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Facts) != 1 {
		t.Fatal(result)
	}
	value, err := pythonCanonicalJSON(result.Facts[0].Qualifiers)
	if err != nil || value != `{"n":9007199254740993}` {
		t.Fatalf("%s %v", value, err)
	}
	if _, err := parseExtractionJSON("null"); err == nil {
		t.Fatal("accepted null extraction")
	}
}

func TestSearchTiesPreserveRetrievalOrder(t *testing.T) {
	driver := queryFunc(func(_ context.Context, q string, _ map[string]any) (any, error) {
		ids := []string{"first", "second"}
		if strings.Contains(q, "@0@") {
			ids = []string{"second", "first"}
		}
		return []map[string]any{{"uuid": ids[0]}, {"uuid": ids[1]}}, nil
	})
	for i := 0; i < 50; i++ {
		result, err := HybridSearch(context.Background(), driver, "query", []float64{1}, nil, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Edges) != 2 || result.Edges[0].UUID != "first" {
			t.Fatalf("unstable ordering: %+v", result.Edges)
		}
	}
}

func TestExplicitZeroSearchTuning(t *testing.T) {
	cfg := DefaultSearchConfig()
	cfg.MMRLambda = 0
	cfg.AdmitCosine = 0
	cfg.SpreadingActivationWeight = 0
	got := normalizeSearchConfig(&cfg)
	if got.MMRLambda != 0 || got.AdmitCosine != 0 || got.SpreadingActivationWeight != 0 {
		t.Fatal("explicit zero replaced by defaults")
	}
}

func TestReadModelKeepsStructuredRows(t *testing.T) {
	rows := []map[string]any{{"uuid": "fact", "attributes": map[string]any{"nested": []map[string]any{{"ok": true}}}, "fact_embedding": []float64{1, 2}}}
	value := ReadModelJSONable(rows)
	list, ok := value.([]any)
	if !ok || len(list) != 1 {
		t.Fatalf("rows flattened to %T: %v", value, value)
	}
	row := list[0].(map[string]any)
	if _, exists := row["fact_embedding"]; exists {
		t.Fatal("embedding exposed")
	}
	nested := row["attributes"].(map[string]any)["nested"].([]any)
	if nested[0].(map[string]any)["ok"] != true {
		t.Fatal("nested rows lost")
	}
}

func TestContradictionIndexesPreserveJSONIntegerType(t *testing.T) {
	got := parseContradictionsJSON(`{"invalidated_indexes":[0,1.0,"2",true,false,99]}`, 3)
	if len(got) != 3 || got[0] != 0 || got[1] != 1 || got[2] != 0 {
		t.Fatalf("indexes=%v", got)
	}
}

func TestTraitCandidatesExcludeIdentityPredicates(t *testing.T) {
	for predicate := range IdentityPredicates {
		edge := NewEntityEdge("subject", "object", predicate, "g")
		edge.ReinforcementCount = 3
		if got := traitCandidatesForSubject("subject", []EntityEdge{edge}); len(got) != 0 {
			t.Fatalf("identity became trait: %s", predicate)
		}
	}
}

func TestSilentResurrectionRestrictsCandidatesBeforeRetrieval(t *testing.T) {
	called := false
	driver := queryFunc(func(_ context.Context, query string, vars map[string]any) (any, error) {
		called = true
		if !strings.Contains(query, "uuid IN $allowed_edge_uuids") {
			t.Fatal("unrestricted silent-memory query")
		}
		allowed := vars["allowed_edge_uuids"].([]string)
		if len(allowed) != 1 || allowed[0] != "authorized" {
			t.Fatalf("allowed=%v", allowed)
		}
		return []map[string]any{}, nil
	})
	resurrectSilentMemory(context.Background(), driver, []float64{1}, nil, .5, nil, nil, []string{"authorized"})
	if !called {
		t.Fatal("retrieval not exercised")
	}
}
