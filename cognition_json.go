package surriti

import (
	"regexp"
	"strings"
)

var jsonFenceRE = regexp.MustCompile("(?m)^```(?:json)?\\s*|\\s*```$")
var jsonPayloadRE = regexp.MustCompile("(?s)[\\[{].*[\\]}]")
var snakeCaseRE = regexp.MustCompile("[^a-zA-Z0-9]+")

func ParseJSONLoose(raw string) any {
	if raw == "" {
		return nil
	}
	text := strings.TrimSpace(jsonFenceRE.ReplaceAllString(raw, ""))
	if text == "" {
		return nil
	}
	var out any
	if decodeJSONNumbers([]byte(text), &out) == nil {
		return out
	}
	if m := jsonPayloadRE.FindString(text); m != "" {
		if decodeJSONNumbers([]byte(m), &out) == nil {
			return out
		}
	}
	return nil
}

func SnakeCase(name string) string {
	s := strings.ToLower(strings.Trim(snakeCaseRE.ReplaceAllString(strings.TrimSpace(name), "_"), "_"))
	if s == "" {
		return "unknown"
	}
	return s
}
