package surriti

import (
	"encoding/json"
	"reflect"
	"time"
)

type ComparisonOperator string

const (
	OpEQ        ComparisonOperator = "="
	OpNE        ComparisonOperator = "<>"
	OpGT        ComparisonOperator = ">"
	OpLT        ComparisonOperator = "<"
	OpGTE       ComparisonOperator = ">="
	OpLTE       ComparisonOperator = "<="
	OpIsNull    ComparisonOperator = "IS NULL"
	OpIsNotNull ComparisonOperator = "IS NOT NULL"
)

type DateFilter struct {
	Date *time.Time
	Op   ComparisonOperator
}

type PropertyFilter struct {
	Name  string
	Value any
	Op    ComparisonOperator
}

type SearchFilters struct {
	NodeLabels        []string
	EdgeTypes         []string
	EdgeUUIDs         []string
	ValidAt           [][]DateFilter
	InvalidAt         [][]DateFilter
	CreatedAt         [][]DateFilter
	ExpiredAt         [][]DateFilter
	PropertyFilters   []PropertyFilter
	EdgeMemoryClasses []string
}

func dateClauseMatches(value *time.Time, clauses [][]DateFilter) bool {
	if len(clauses) == 0 {
		return true
	}
	for _, window := range clauses {
		ok := true
		for _, clause := range window {
			if !evalDate(value, clause) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func evalDate(value *time.Time, clause DateFilter) bool {
	if clause.Op == OpIsNull {
		return value == nil
	}
	if clause.Op == OpIsNotNull {
		return value != nil
	}
	if value == nil || clause.Date == nil {
		return false
	}
	a := value.UTC()
	b := clause.Date.UTC()
	switch clause.Op {
	case OpEQ:
		return a.Equal(b)
	case OpNE:
		return !a.Equal(b)
	case OpGT:
		return a.After(b)
	case OpLT:
		return a.Before(b)
	case OpGTE:
		return a.After(b) || a.Equal(b)
	case OpLTE:
		return a.Before(b) || a.Equal(b)
	default:
		return true
	}
}

func EdgePassesFilters(row map[string]any, f *SearchFilters) bool {
	if f == nil {
		return true
	}
	if f.EdgeTypes != nil && !containsString(f.EdgeTypes, asString(row["name"])) {
		return false
	}
	if f.EdgeUUIDs != nil && !containsString(f.EdgeUUIDs, asString(row["uuid"])) {
		return false
	}
	for _, item := range []struct {
		name    string
		clauses [][]DateFilter
	}{
		{"valid_at", f.ValidAt}, {"invalid_at", f.InvalidAt}, {"created_at", f.CreatedAt}, {"expired_at", f.ExpiredAt},
	} {
		if !dateClauseMatches(asTimePtr(row[item.name]), item.clauses) {
			return false
		}
	}
	for _, pf := range f.PropertyFilters {
		if !evalProperty(row, pf) {
			return false
		}
	}
	if f.EdgeMemoryClasses != nil {
		cls := "objective"
		if attrs, ok := row["attributes"].(map[string]any); ok {
			if v := lowerTrim(asString(attrs["memory_class"])); v != "" {
				cls = v
			}
		}
		if !containsString(f.EdgeMemoryClasses, cls) {
			return false
		}
	}
	return true
}

func NodePassesFilters(row map[string]any, f *SearchFilters) bool {
	if f == nil {
		return true
	}
	if f.NodeLabels != nil {
		labels := asStringSlice(row["labels"])
		found := false
		for _, l := range labels {
			if containsString(f.NodeLabels, l) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	for _, pf := range f.PropertyFilters {
		if !evalProperty(row, pf) {
			return false
		}
	}
	return true
}

func evalProperty(row map[string]any, pf PropertyFilter) bool {
	op := pf.Op
	if op == "" {
		// Python PropertyFilter defaults to equality.
		op = OpEQ
	}
	actual, ok := row[pf.Name]
	if !ok || actual == nil {
		if attrs, aok := row["attributes"].(map[string]any); aok {
			actual, ok = attrs[pf.Name]
		}
	}
	if op == OpIsNull {
		return !ok || actual == nil
	}
	if op == OpIsNotNull {
		return ok && actual != nil
	}
	if !ok || actual == nil {
		return false
	}
	if op == OpEQ {
		return reflect.DeepEqual(actual, pf.Value) || numericEqual(actual, pf.Value)
	}
	if op == OpNE {
		return !(reflect.DeepEqual(actual, pf.Value) || numericEqual(actual, pf.Value))
	}
	if a, b, ok := numericPair(actual, pf.Value); ok {
		switch op {
		case OpGT:
			return a > b
		case OpLT:
			return a < b
		case OpGTE:
			return a >= b
		case OpLTE:
			return a <= b
		}
	}
	if a, ok := actual.(string); ok {
		if b, bok := pf.Value.(string); bok {
			switch op {
			case OpGT:
				return a > b
			case OpLT:
				return a < b
			case OpGTE:
				return a >= b
			case OpLTE:
				return a <= b
			}
		}
	}
	if a := asTimePtr(actual); a != nil {
		if b := asTimePtr(pf.Value); b != nil {
			switch op {
			case OpGT:
				return a.After(*b)
			case OpLT:
				return a.Before(*b)
			case OpGTE:
				return a.After(*b) || a.Equal(*b)
			case OpLTE:
				return a.Before(*b) || a.Equal(*b)
			}
		}
	}
	// Known comparisons between incompatible types fail, as in Python.
	switch op {
	case OpGT, OpLT, OpGTE, OpLTE:
		return false
	}
	return true
}

func containsString(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func asString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	// SurrealDB record IDs are SDK value types rather than plain strings.
	// fmt.Sprint/stringFromAny preserves their canonical "table:id" form.
	return stringFromAny(v)
}

func asStringSlice(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, v := range x {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func asTimePtr(v any) *time.Time {
	switch x := v.(type) {
	case time.Time:
		t := x
		return &t
	case *time.Time:
		return x
	case string:
		if t, err := time.Parse(time.RFC3339Nano, x); err == nil {
			return &t
		}
	}
	return nil
}

func numericPair(a, b any) (float64, float64, bool) {
	af, aok := toFloat(a)
	bf, bok := toFloat(b)
	return af, bf, aok && bok
}
func numericEqual(a, b any) bool {
	af, bf, ok := numericPair(a, b)
	return ok && af == bf
}
func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case int:
		return float64(x), true
	case int8:
		return float64(x), true
	case int16:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint8:
		return float64(x), true
	case uint16:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	case float32:
		return float64(x), true
	case float64:
		return x, true
	default:
		return 0, false
	}
}
