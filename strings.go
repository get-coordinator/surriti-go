package surriti

import "strings"

func trim(s string) string { return strings.TrimSpace(s) }
func lowerTrim(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
func join(parts []string, sep string) string { return strings.Join(parts, sep) }
