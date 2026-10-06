package surriti

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func integrationDriver(t *testing.T) *SurrealDriver {
	t.Helper()
	if os.Getenv("SURRITI_INTEGRATION") != "1" {
		t.Skip("set SURRITI_INTEGRATION=1 to run live SurrealDB integration tests")
	}
	cfg := DefaultDriverConfig()
	cfg.URL = os.Getenv("SURRITI_SURREAL_URL")
	if cfg.URL == "" {
		cfg.URL = "ws://127.0.0.1:8000/rpc"
	}
	cfg.Namespace = os.Getenv("SURRITI_SURREAL_NS")
	if cfg.Namespace == "" {
		cfg.Namespace = "surriti_go"
	}
	// Never initialize or clear a caller's existing database.
	cfg.Database = "surriti_go_test_" + strings.ReplaceAll(newUUID(), "-", "")
	cfg.Username = os.Getenv("SURRITI_SURREAL_USER")
	if cfg.Username == "" {
		cfg.Username = "root"
	}
	cfg.Password = os.Getenv("SURRITI_SURREAL_PASS")
	if cfg.Password == "" {
		cfg.Password = "root"
	}
	d, err := NewDefaultSurrealDriver(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := d.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if err := d.InitSchema(ctx); err != nil {
		_ = d.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		if err := d.Connect(cctx); err == nil {
			_, _ = d.Query(cctx, "REMOVE DATABASE "+cfg.Database+";", nil)
		}
		_ = d.Close(cctx)
	})
	return d
}

func TestIntegrationSchemaAndTripletTemporalParity(t *testing.T) {
	d := integrationDriver(t)
	t.Setenv("SURRITI_ALLOW_DESTRUCTIVE", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := d.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	s, err := NewSurriti(d, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	first, err := s.AddTriplet(ctx, AddTripletRequest{SubjectName: "Alice", Predicate: "lives_in", ObjectName: "Philadelphia", GroupID: "g"})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Edges) != 1 {
		t.Fatalf("first edges=%d", len(first.Edges))
	}
	second, err := s.AddTriplet(ctx, AddTripletRequest{SubjectName: "Alice", Predicate: "lives_in", ObjectName: "Los Angeles", GroupID: "g"})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Edges) != 1 {
		t.Fatalf("second edges=%d", len(second.Edges))
	}
	if len(second.InvalidatedEdges) != 1 {
		t.Fatalf("invalidated=%d want 1", len(second.InvalidatedEdges))
	}
	alice := second.Nodes[0]
	current, err := s.GetCurrentFact(ctx, alice.UUID, "lives_in", "g")
	if err != nil {
		t.Fatal(err)
	}
	if current == nil {
		t.Fatal("missing current lives_in fact")
	}
	if current.TargetNodeUUID != second.Nodes[1].UUID {
		t.Fatalf("target=%q want %q", current.TargetNodeUUID, second.Nodes[1].UUID)
	}
}

func TestIntegrationEpisodeRetryIsIdempotent(t *testing.T) {
	d := integrationDriver(t)
	t.Setenv("SURRITI_ALLOW_DESTRUCTIVE", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := d.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	f := NewExtractedFact("Alice", "works_at", "Acme")
	f.Fact = "Alice works at Acme."
	llm := NewScriptedLLMClient([]ScriptedResponse{{Entities: []ExtractedEntity{NewExtractedEntity("Alice"), NewExtractedEntity("Acme")}, Facts: []ExtractedFact{f}}})
	s, err := NewSurriti(d, &SurritiOptions{LLM: llm})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	id := "episode-stable"
	res, err := s.AddEpisode(ctx, AddEpisodeRequest{Name: "turn", EpisodeBody: "Alice works at Acme.", GroupID: "g", UUID: &id})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Edges) != 1 {
		t.Fatalf("edges=%d want 1", len(res.Edges))
	}
	retry, err := s.AddEpisode(ctx, AddEpisodeRequest{Name: "turn", EpisodeBody: "Alice works at Acme.", GroupID: "g", UUID: &id})
	if err != nil {
		t.Fatal(err)
	}
	if len(retry.Edges) != 0 || len(retry.Nodes) != 0 || len(retry.EpisodicEdges) != 0 {
		t.Fatalf("completed retry was not no-op: %+v", retry)
	}
	raw, err := d.Query(ctx, "SELECT uuid FROM mentions WHERE group_id = $g;", map[string]any{"g": "g"})
	if err != nil {
		t.Fatal(err)
	}
	rows := UnwrapRows(raw)
	if len(rows) != 2 {
		t.Fatalf("mention rows=%v want 2", rows)
	}
}

func TestIntegrationParticipantIsolationKeepsCanonicalFact(t *testing.T) {
	d := integrationDriver(t)
	t.Setenv("SURRITI_ALLOW_DESTRUCTIVE", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := d.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	f := NewExtractedFact("Michael", "owns_pet", "Duke")
	f.Fact = "Michael owns Duke."
	llm := NewScriptedLLMClient([]ScriptedResponse{{Entities: []ExtractedEntity{NewExtractedEntity("Duke")}, Facts: []ExtractedFact{f}}})
	s, err := NewSurriti(d, &SurritiOptions{LLM: llm})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	speaker := "user-a"
	speakerName := "Michael"
	participant := "user-b"
	id := "participant-ep"
	res, err := s.AddEpisode(ctx, AddEpisodeRequest{Name: "turn", EpisodeBody: "I own Duke.", GroupID: "g", UUID: &id, SpeakerID: &speaker, SpeakerName: &speakerName, ParticipantIDs: []string{participant}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Edges) != 1 {
		t.Fatalf("edges=%d", len(res.Edges))
	}
	factID := res.Edges[0].UUID
	allowed, err := s.RecallForParticipant(ctx, "Duke", participant, "fast", 10, false)
	if err != nil || len(allowed.Edges) != 1 {
		t.Fatalf("participant recall: %+v %v", allowed, err)
	}
	denied, err := s.RecallForParticipant(ctx, "Duke", "outsider", "fast", 10, false)
	if err != nil || len(denied.Edges) != 0 {
		t.Fatalf("private fact exposed: %+v %v", denied, err)
	}
	granted, err := s.GrantMemoryToParticipant(ctx, speaker, "recipient", factID)
	if err != nil || !granted {
		t.Fatalf("grant: %v %v", granted, err)
	}
	deniedGrant, err := s.GrantMemoryToParticipant(ctx, "outsider", "recipient", factID)
	if err != nil || deniedGrant {
		t.Fatalf("unauthorized grant: %v %v", deniedGrant, err)
	}
	revoked, err := s.RevokeMemoryFromParticipant(ctx, speaker, "recipient", factID)
	if err != nil || revoked != 1 {
		t.Fatalf("revoke: %v %v", revoked, err)
	}
	count, err := s.ForgetMemoryForParticipant(ctx, "g", participant, factID, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("forgot refs=%d want 1", count)
	}
	edge, err := s.GetEntityEdge(ctx, factID)
	if err != nil {
		t.Fatal(err)
	}
	if edge == nil {
		t.Fatal("participant forget deleted canonical fact")
	}
}

func TestIntegrationTimestampRoundTrip(t *testing.T) {
	d := integrationDriver(t)
	want := time.Date(2020, 2, 3, 4, 5, 6, 123456000, time.UTC)
	raw, err := d.Query(context.Background(), "RETURN {created_at: $when, nested: {valid_at: $when}};", map[string]any{"when": want})
	if err != nil {
		t.Fatal(err)
	}
	rows := UnwrapRows(raw)
	if len(rows) != 1 {
		t.Fatalf("rows=%#v", raw)
	}
	got := coerceTime(rows[0]["created_at"])
	if got == nil || !got.Equal(want) {
		t.Fatalf("timestamp lost: %T %#v", rows[0]["created_at"], rows[0]["created_at"])
	}
	nested := mapFromAny(rows[0]["nested"])
	if t2 := coerceTime(nested["valid_at"]); t2 == nil || !t2.Equal(want) {
		t.Fatal("nested timestamp lost")
	}
}

func TestIntegrationCognitionAndMemoryPack(t *testing.T) {
	d := integrationDriver(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	f := NewExtractedFact("Alice", "likes", "running")
	llm := NewScriptedLLMClient([]ScriptedResponse{{Entities: []ExtractedEntity{NewExtractedEntity("Alice"), NewExtractedEntity("running")}, Facts: []ExtractedFact{f}}})
	s, err := NewSurriti(d, &SurritiOptions{LLM: llm, ProfileRefresh: "sync"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	ep, err := s.AddEpisode(ctx, AddEpisodeRequest{Name: "intent", EpisodeBody: "I want to improve distance running. Alice likes running.", GroupID: "source"})
	if err != nil {
		t.Fatal(err)
	}
	metrics := RunCognitionPass(ctx, d, llm, s.Embedder, "source", []string{ep.Episode.UUID}, DefaultCognitionConfig(), 3)
	if !metrics.Processed {
		t.Fatalf("cognition failures: %+v", metrics)
	}
	view, err := ReadCognition(ctx, d, "goals", "source", 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := view["goals"].([]any); !ok {
		t.Fatalf("read-model goals are %T", view["goals"])
	}
	stored, err := s.GetEpisode(ctx, ep.Episode.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.CognitionProcessedAt == nil || stored.CognitionVersion == nil {
		t.Fatal("missing cognition marker")
	}
	path := filepath.Join(t.TempDir(), "memory.zip")
	exported, err := s.ExportMemoryPack(ctx, "source", path, "always", 2)
	if err != nil {
		t.Fatal(err)
	}
	if exported.Counts["edges"] < 1 {
		t.Fatal("no edges exported")
	}
	if v := ValidatePackZip(path); !v.OK {
		t.Fatal(v.Errors)
	}
	imported, err := s.ImportMemoryPack(ctx, path, "target", "merge")
	if err != nil {
		t.Fatal(err)
	}
	if !imported.Validation.OK || imported.Counts["edges"] != exported.Counts["edges"] {
		t.Fatalf("import=%+v", imported)
	}
	if _, err = s.ImportMemoryPack(ctx, path, "target", "merge"); err != nil {
		t.Fatal("repeat import:", err)
	}
	raw, err := d.Query(ctx, "SELECT * FROM relates_to WHERE group_id = 'target';", nil)
	if err != nil {
		t.Fatal(err)
	}
	rows := UnwrapRows(raw)
	if len(rows) != exported.Counts["edges"] {
		t.Fatalf("import duplicated edges: %d", len(rows))
	}
	for _, row := range rows {
		if len(rowVector(row["fact_embedding"])) != s.Embedder.EmbeddingDim() {
			t.Fatal("pack lost embedding")
		}
		if coerceTime(row["created_at"]) == nil {
			t.Fatal("pack lost timestamp")
		}
	}
}

func TestIntegrationPythonCompatibility(t *testing.T) {
	reference := os.Getenv("SURRITI_PYTHON_REFERENCE")
	if reference == "" {
		t.Skip("set SURRITI_PYTHON_REFERENCE to the frozen Python package directory")
	}
	d := integrationDriver(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bridge := func(action, path string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "python3", "scripts/python_bridge.py", d.Config().Database, action, path)
		cmd.Env = append(os.Environ(), "PYTHONPATH="+reference)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("Python %s: %v\n%s", action, err, output)
		}
	}
	dir := t.TempDir()
	ids := filepath.Join(dir, "ids.json")
	bridge("seed", ids)
	var state struct {
		Subject   string
		FirstEdge string `json:"first_edge"`
	}
	if err := readJSONFile(ids, &state); err != nil {
		t.Fatal(err)
	}
	off := false
	s, err := NewSurriti(d, &SurritiOptions{CognitionEnabled: &off, ProfileRefresh: "off", AliasResolution: &off})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	old, err := s.GetEntityEdge(ctx, state.FirstEdge)
	if err != nil {
		t.Fatal(err)
	}
	if old == nil || old.ValidAt == nil || old.ValidAt.Nanosecond() != 123456000 {
		t.Fatal("cannot read Python timestamp")
	}
	at := time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)
	result, err := s.AddTriplet(ctx, AddTripletRequest{SubjectName: "STRASSE", Predicate: "works_at", ObjectName: "Beta", GroupID: "bridge", ValidAt: &at})
	if err != nil {
		t.Fatal(err)
	}
	if result.Nodes[0].UUID != state.Subject || len(result.InvalidatedEdges) != 1 {
		t.Fatal("Python entity/temporal lineage not reused")
	}
	pyPack := filepath.Join(dir, "python.zip")
	bridge("verify_export", pyPack)
	imported, err := s.ImportMemoryPack(ctx, pyPack, "go_import", "merge")
	if err != nil || !imported.Validation.OK {
		t.Fatalf("Python pack import: %+v %v", imported, err)
	}
	goPack := filepath.Join(dir, "go.zip")
	if _, err = s.ExportMemoryPack(ctx, "go_import", goPack, "always", 1); err != nil {
		t.Fatal(err)
	}
	bridge("verify_import", goPack)
}

func TestIntegrationSelfEpisodeSchedulesCognition(t *testing.T) {
	d := integrationDriver(t)
	config := DefaultCognitionConfig()
	config.IdleSeconds = 3600
	s, err := NewSurriti(d, &SurritiOptions{Cognition: &config, ProfileRefresh: "off"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err = s.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	result, err := s.AddSelfEpisode(ctx, EpisodeSelfObservation, "I was too verbose.", "self", "", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	scheduler := s.CognitionScheduler()
	scheduler.mu.Lock()
	state := scheduler.states["self"]
	notified := state != nil && containsString(state.PendingEpisodeUUIDs, result.Episode.UUID)
	scheduler.mu.Unlock()
	if !notified {
		t.Fatal("self episode never notified cognition")
	}
}
