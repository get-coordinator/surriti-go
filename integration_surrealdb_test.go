package surriti

import (
	"context"
	"os"
	"testing"
	"time"
)

func integrationDriver(t *testing.T) *SurrealDriver {
	t.Helper()
	if os.Getenv("SURRITI_INTEGRATION") != "1" {
		t.Skip("set SURRITI_INTEGRATION=1 to run live SurrealDB integration tests")
	}
	cfg:=DefaultDriverConfig()
	cfg.URL=os.Getenv("SURRITI_SURREAL_URL")
	if cfg.URL==""{cfg.URL="ws://127.0.0.1:8000/rpc"}
	cfg.Namespace=os.Getenv("SURRITI_SURREAL_NS");if cfg.Namespace==""{cfg.Namespace="surriti_go"}
	cfg.Database=os.Getenv("SURRITI_SURREAL_DB");if cfg.Database==""{cfg.Database="integration"}
	cfg.Username=os.Getenv("SURRITI_SURREAL_USER");if cfg.Username==""{cfg.Username="root"}
	cfg.Password=os.Getenv("SURRITI_SURREAL_PASS");if cfg.Password==""{cfg.Password="root"}
	d,err:=NewDefaultSurrealDriver(cfg);if err!=nil{t.Fatal(err)}
	ctx,cancel:=context.WithTimeout(context.Background(),20*time.Second);defer cancel()
	if err:=d.Connect(ctx);err!=nil{t.Fatal(err)}
	if err:=d.InitSchema(ctx);err!=nil{_ = d.Close(ctx);t.Fatal(err)}
	t.Cleanup(func(){
		cctx,ccancel:=context.WithTimeout(context.Background(),10*time.Second);defer ccancel()
		_ = d.Close(cctx)
	})
	return d
}

func TestIntegrationSchemaAndTripletTemporalParity(t *testing.T){
	d:=integrationDriver(t)
	t.Setenv("SURRITI_ALLOW_DESTRUCTIVE","1")
	ctx,cancel:=context.WithTimeout(context.Background(),30*time.Second);defer cancel()
	if err:=d.Clear(ctx);err!=nil{t.Fatal(err)}
	s,err:=NewSurriti(d,nil);if err!=nil{t.Fatal(err)}
	first,err:=s.AddTriplet(ctx,AddTripletRequest{SubjectName:"Alice",Predicate:"lives_in",ObjectName:"Philadelphia",GroupID:"g"})
	if err!=nil{t.Fatal(err)}
	if len(first.Edges)!=1{t.Fatalf("first edges=%d",len(first.Edges))}
	second,err:=s.AddTriplet(ctx,AddTripletRequest{SubjectName:"Alice",Predicate:"lives_in",ObjectName:"Los Angeles",GroupID:"g"})
	if err!=nil{t.Fatal(err)}
	if len(second.Edges)!=1{t.Fatalf("second edges=%d",len(second.Edges))}
	if len(second.InvalidatedEdges)!=1{t.Fatalf("invalidated=%d want 1",len(second.InvalidatedEdges))}
	alice:=second.Nodes[0]
	current,err:=s.GetCurrentFact(ctx,alice.UUID,"lives_in","g");if err!=nil{t.Fatal(err)}
	if current==nil{t.Fatal("missing current lives_in fact")}
	if current.TargetNodeUUID!=second.Nodes[1].UUID{t.Fatalf("target=%q want %q",current.TargetNodeUUID,second.Nodes[1].UUID)}
}

func TestIntegrationEpisodeRetryIsIdempotent(t *testing.T){
	d:=integrationDriver(t)
	t.Setenv("SURRITI_ALLOW_DESTRUCTIVE","1")
	ctx,cancel:=context.WithTimeout(context.Background(),30*time.Second);defer cancel()
	if err:=d.Clear(ctx);err!=nil{t.Fatal(err)}
	f:=NewExtractedFact("Alice","works_at","Acme")
	f.Fact="Alice works at Acme."
	llm:=NewScriptedLLMClient([]ScriptedResponse{{Entities:[]ExtractedEntity{NewExtractedEntity("Alice"),NewExtractedEntity("Acme")},Facts:[]ExtractedFact{f}}})
	s,err:=NewSurriti(d,&SurritiOptions{LLM:llm});if err!=nil{t.Fatal(err)}
	id:="episode-stable"
	res,err:=s.AddEpisode(ctx,AddEpisodeRequest{Name:"turn",EpisodeBody:"Alice works at Acme.",GroupID:"g",UUID:&id})
	if err!=nil{t.Fatal(err)}
	if len(res.Edges)!=1{t.Fatalf("edges=%d want 1",len(res.Edges))}
	retry,err:=s.AddEpisode(ctx,AddEpisodeRequest{Name:"turn",EpisodeBody:"Alice works at Acme.",GroupID:"g",UUID:&id})
	if err!=nil{t.Fatal(err)}
	if len(retry.Edges)!=0||len(retry.Nodes)!=0||len(retry.EpisodicEdges)!=0{t.Fatalf("completed retry was not no-op: %+v",retry)}
	raw,err:=d.Query(ctx,"SELECT count() AS cnt FROM mentions WHERE group_id = $g;",map[string]any{"g":"g"});if err!=nil{t.Fatal(err)}
	rows:=UnwrapRows(raw);if len(rows)==0||intFromAny(rows[0]["cnt"])!=2{t.Fatalf("mention count rows=%v",rows)}
}

func TestIntegrationParticipantIsolationKeepsCanonicalFact(t *testing.T){
	d:=integrationDriver(t)
	t.Setenv("SURRITI_ALLOW_DESTRUCTIVE","1")
	ctx,cancel:=context.WithTimeout(context.Background(),30*time.Second);defer cancel()
	if err:=d.Clear(ctx);err!=nil{t.Fatal(err)}
	f:=NewExtractedFact("Michael","owns_pet","Duke");f.Fact="Michael owns Duke."
	llm:=NewScriptedLLMClient([]ScriptedResponse{{Entities:[]ExtractedEntity{NewExtractedEntity("Duke")},Facts:[]ExtractedFact{f}}})
	s,err:=NewSurriti(d,&SurritiOptions{LLM:llm});if err!=nil{t.Fatal(err)}
	speaker:="user-a";speakerName:="Michael";participant:="user-b";id:="participant-ep"
	res,err:=s.AddEpisode(ctx,AddEpisodeRequest{Name:"turn",EpisodeBody:"I own Duke.",GroupID:"g",UUID:&id,SpeakerID:&speaker,SpeakerName:&speakerName,ParticipantIDs:[]string{participant}})
	if err!=nil{t.Fatal(err)}
	if len(res.Edges)!=1{t.Fatalf("edges=%d",len(res.Edges))}
	factID:=res.Edges[0].UUID
	count,err:=s.ForgetMemoryForParticipant(ctx,"g",participant,factID,nil,nil);if err!=nil{t.Fatal(err)}
	if count!=1{t.Fatalf("forgot refs=%d want 1",count)}
	edge,err:=s.GetEntityEdge(ctx,factID);if err!=nil{t.Fatal(err)}
	if edge==nil{t.Fatal("participant forget deleted canonical fact")}
}
