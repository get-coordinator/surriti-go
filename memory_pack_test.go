package surriti

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func writeTestPack(t *testing.T, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pack.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	for name, body := range entries {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPackValidationRejectsMissingChecksumTargetAndTraversal(t *testing.T) {
	entries := map[string]string{}
	for _, name := range memoryPackFiles {
		entries[name] = ""
	}
	entries["manifest.json"] = `{"format":"surriti.memory-pack","version":1}`
	entries["checksums.json"] = `{"missing.jsonl":{"sha256":"abc","rows":1}}`
	if result := ValidatePackZip(writeTestPack(t, entries)); result.OK || len(result.Errors) == 0 {
		t.Fatal(result)
	}
	entries["checksums.json"] = `{}`
	entries["../escape"] = "bad"
	if result := ValidatePackZip(writeTestPack(t, entries)); result.OK {
		t.Fatal("accepted traversal")
	}
	delete(entries, "../escape")
	entries["../escape/"] = ""
	if result := ValidatePackZip(writeTestPack(t, entries)); result.OK {
		t.Fatal("accepted directory traversal")
	}
}

func TestReadPackPreservesNumbersAndRejectsNull(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rows.jsonl")
	if err := os.WriteFile(path, []byte("{\"qualifiers\":{\"n\":9007199254740993}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	rows, err := readJSONL(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pythonCanonicalJSON(rows[0]["qualifiers"])
	if err != nil || got != `{"n":9007199254740993}` {
		t.Fatal(got, err)
	}
	if err := os.WriteFile(path, []byte("null\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readJSONL(path); err == nil {
		t.Fatal("accepted null row")
	}
}
