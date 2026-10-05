package surriti

import "testing"

func TestRepairFactDropsLocationFiller(t *testing.T) {
	f := NewExtractedFact("Michael", "lives_in", "world")
	if RepairFact(f, "", "") != nil { t.Fatal("expected drop") }
}

func TestRepairFactRewritesIdentitySelfLoop(t *testing.T) {
	f := NewExtractedFact("Auley", "is_named", "Auley")
	got := RepairFact(f, "default", "")
	if got == nil || got.Subject != "default" { t.Fatalf("got %#v", got) }
}

func TestRepairFactDropsNonIdentitySelfLoop(t *testing.T) {
	f := NewExtractedFact("Michael", "knows", "Michael")
	if RepairFact(f, "", "") != nil { t.Fatal("expected drop") }
}
