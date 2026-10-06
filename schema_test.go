package surriti

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestSchemaDDLUsesConfiguredEmbeddingDimensionAndManagedTables(t *testing.T) {
	ddl, err := SchemaDDL(256)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{
		"HNSW DIMENSION 256",
		"DEFINE TABLE IF NOT EXISTS episode",
		"DEFINE TABLE IF NOT EXISTS entity_alias",
		"DEFINE TABLE IF NOT EXISTS memory_ref",
		"DEFINE TABLE IF NOT EXISTS relation_frame",
		"memory_ref_viewer_fact_active_idx",
		"relates_to_canonical_idx",
		"cognition_processed_at",
	} {
		if !strings.Contains(ddl, s) {
			t.Fatalf("DDL missing %q", s)
		}
	}
}

func TestBackfillFactKeysPreservesQualifierIdentity(t *testing.T) {
	key := MakeFactKey("g", "s", "lives_in", "o", QualifierHash(map[string]any{"season": "winter"}))
	if key != "g::s::lives_in::o::a4709ad6d8fc42b7" {
		t.Fatalf("key=%q", key)
	}
}

func TestSchemaCompatibilityUsesPermissiveThenStrictFields(t *testing.T) {
	if !strings.Contains(schemaCompatibilityPreflight, "TYPE option<object>") {
		t.Fatal("missing permissive preflight")
	}
	if !strings.Contains(schemaCompatibilityStrict, "TYPE object FLEXIBLE DEFAULT {}") {
		t.Fatal("missing strict phase")
	}
	if len(schemaCompatibilityBackfills) != 13 {
		t.Fatalf("backfills=%d", len(schemaCompatibilityBackfills))
	}
}

func TestSchemaInitErrorClassification(t *testing.T) {
	err := schemaInitError(context.Background(), errors.New("boom"))
	if !errors.Is(err, ErrSchema) {
		t.Fatalf("err=%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = schemaInitError(ctx, errors.New("boom"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestSchemaMatchesFrozenPython(t *testing.T) {
	expected, err := os.ReadFile("testdata/schema.surql")
	if err != nil {
		t.Fatal(err)
	}
	actual, err := SchemaDDL(768)
	if err != nil {
		t.Fatal(err)
	}
	if actual != string(expected) {
		t.Fatal("schema differs from frozen Python baseline")
	}
}
