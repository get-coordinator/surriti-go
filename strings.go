package surriti

import (
	"golang.org/x/text/cases"
	"sort"
	"strings"
)

func trim(s string) string                   { return strings.TrimSpace(s) }
func lowerTrim(s string) string              { return strings.ToLower(strings.TrimSpace(s)) }
func join(parts []string, sep string) string { return strings.Join(parts, sep) }

// Python entity keys use full Unicode case folding (ß == ss, ς == σ).
// Casers may own mutable transformer state, so each call owns its caser.
func casefold(s string) string { return cases.Fold().String(s) }

func prefixRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	for i := range s {
		if n == 0 {
			return s[:i]
		}
		n--
	}
	return s
}

func sortedStringsUnique(values []string) []string {
	set := map[string]struct{}{}
	for _, v := range values {
		set[v] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
