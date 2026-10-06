package surriti

import (
	"fmt"
	"reflect"

	"github.com/surrealdb/surrealdb.go/pkg/models"
)

// UnwrapRows canonicalizes the result shapes Surriti historically accepted:
// a flat SDK-2-style row list, the older list-of-lists shape, and the legacy
// {"result": [...]} statement wrapper. For multi-statement results the last
// statement is authoritative, matching the Python implementation.
func UnwrapRows(value any) []map[string]any {
	if value == nil {
		return []map[string]any{}
	}
	if row, ok := toStringAnyMap(value); ok {
		if result, exists := row["result"]; exists {
			return mapsFromAnySlice(result)
		}
		return []map[string]any{row}
	}

	rv := reflect.ValueOf(value)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return []map[string]any{}
	}
	if rv.Len() == 0 {
		return []map[string]any{}
	}

	flat := make([]map[string]any, 0, rv.Len())
	flatOK := true
	allWithoutResult := true
	for i := 0; i < rv.Len(); i++ {
		row, ok := toStringAnyMap(rv.Index(i).Interface())
		if !ok {
			flatOK = false
			break
		}
		if _, exists := row["result"]; exists {
			allWithoutResult = false
		}
		flat = append(flat, row)
	}
	if flatOK && allWithoutResult {
		return flat
	}

	last := rv.Index(rv.Len() - 1).Interface()
	if row, ok := toStringAnyMap(last); ok {
		if result, exists := row["result"]; exists {
			return mapsFromAnySlice(result)
		}
	}
	if rows := mapsFromAnySlice(last); rows != nil {
		return rows
	}
	if flatOK {
		return flat
	}
	return []map[string]any{}
}

func toStringAnyMap(v any) (map[string]any, bool) {
	if v == nil {
		return nil, false
	}
	if m, ok := v.(map[string]any); ok {
		return m, true
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Map || rv.Type().Key().Kind() != reflect.String {
		return nil, false
	}
	m := make(map[string]any, rv.Len())
	iter := rv.MapRange()
	for iter.Next() {
		m[iter.Key().String()] = iter.Value().Interface()
	}
	return m, true
}

func mapsFromAnySlice(v any) []map[string]any {
	if v == nil {
		return []map[string]any{}
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return nil
	}
	out := make([]map[string]any, 0, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		if m, ok := toStringAnyMap(rv.Index(i).Interface()); ok {
			out = append(out, m)
		}
	}
	return out
}

func mapFromAny(v any) map[string]any {
	m, _ := toStringAnyMap(v)
	if m == nil {
		return map[string]any{}
	}
	return m
}

func intFromAny(v any) int {
	if f, ok := toFloat(v); ok {
		return int(f)
	}
	return 0
}

func stringFromAny(v any) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case models.RecordID:
		return (&x).String()
	case *models.RecordID:
		if x == nil {
			return ""
		}
		return x.String()
	default:
		return fmt.Sprint(v)
	}
}
