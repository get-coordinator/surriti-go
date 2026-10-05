package surriti

import "testing"

func TestUnwrapRowsSDK2FlatList(t *testing.T) {
	rows := []map[string]any{{"uuid": "a"}, {"uuid": "b"}}
	got := UnwrapRows(rows)
	if len(got) != 2 || got[1]["uuid"] != "b" {
		t.Fatalf("got=%v", got)
	}
}
func TestUnwrapRowsSDK1ListOfLists(t *testing.T) {
	v := []any{[]map[string]any{{"uuid": "a"}}}
	got := UnwrapRows(v)
	if len(got) != 1 || got[0]["uuid"] != "a" {
		t.Fatalf("got=%v", got)
	}
}
func TestUnwrapRowsLegacyResultWrapper(t *testing.T) {
	v := []any{map[string]any{"result": []map[string]any{{"uuid": "a"}}}}
	got := UnwrapRows(v)
	if len(got) != 1 || got[0]["uuid"] != "a" {
		t.Fatalf("got=%v", got)
	}
}
func TestUnwrapRowsMultiStatementLegacyUsesLast(t *testing.T) {
	v := []any{
		map[string]any{"result": []map[string]any{{"uuid": "first"}}},
		map[string]any{"result": []map[string]any{{"uuid": "last"}}},
	}
	got := UnwrapRows(v)
	if len(got) != 1 || got[0]["uuid"] != "last" {
		t.Fatalf("got=%v", got)
	}
}
func TestUnwrapRowsEmptyAndNil(t *testing.T) {
	if len(UnwrapRows(nil)) != 0 || len(UnwrapRows([]any{})) != 0 {
		t.Fatal("expected empty")
	}
}
func TestUnwrapRowsSingleDict(t *testing.T) {
	got := UnwrapRows(map[string]any{"uuid": "a"})
	if len(got) != 1 || got[0]["uuid"] != "a" {
		t.Fatalf("got=%v", got)
	}
}
