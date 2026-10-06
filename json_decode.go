package surriti

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// Keep JSON integers distinct from floats: qualifier hashes and imported
// identities depend on Python's distinction between 1 and 1.0.
func decodeJSONNumbers(data []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := d.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		if err != nil {
			return err
		}
		return fmt.Errorf("unexpected trailing JSON value")
	}
	return nil
}

// JSON truthiness is part of Python's tolerant extraction contract. In
// particular, nonempty strings (including "false") are true in Python.
func jsonTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case json.Number:
		f, err := x.Float64()
		return err == nil && f != 0
	case []any:
		return len(x) != 0
	case map[string]any:
		return len(x) != 0
	default:
		if f, ok := toFloat(v); ok {
			return f != 0
		}
		return true
	}
}
