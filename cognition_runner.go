package surriti

import (
	"context"
	"time"
)

const CognitionVersion = "2026-08-actr-memory-lifecycle"

type CognitionMetrics struct {
	GroupID string
	Episodes int
	Values map[string]any
	FailedSteps []string
	Processed bool
	DurationMS int64
}

func RunCognitionPass(ctx context.Context,driver Queryer,llm LLMClient,embedder Embedder,groupID string,episodeUUIDs []string,config CognitionConfig,passCount int) CognitionMetrics {
	start:=time.Now()
	m:=CognitionMetrics{GroupID:groupID,Episodes:len(episodeUUIDs),Values:map[string]any{},FailedSteps:[]string{}}
	fail:=func(step string){m.FailedSteps=append(m.FailedSteps,step)}

	if config.AffectExtraction {
		v,err:=TagEpisodeAffect(ctx,driver,groupID,episodeUUIDs);if err!=nil{fail("affect")}else{m.Values["affect_tagged"]=v}
	}
	if config.BeliefExtraction {
		v,err:=TagBeliefs(ctx,driver,groupID,episodeUUIDs);if err!=nil{fail("perspective")}else{m.Values["beliefs_promoted"]=v}
	}
	if v,err:=ReinforceRecentEdges(ctx,driver,groupID,episodeUUIDs);err!=nil{fail("reinforcement")}else{m.Values["edges_reinforced"]=v}
	if v,err:=RefreshAssociativeWeights(ctx,driver,groupID,config.DecayHalfLifeDays);err!=nil{fail("associative")}else{m.Values["weights_refreshed"]=v}
	if config.MemorySilenceEnabled {
		v,err:=SilenceInactiveEdges(ctx,driver,groupID,config.MemorySilenceMinAgeDays,config.MemorySilenceActivation,config.MemorySilenceSweepLimit);if err!=nil{fail("memory_lifecycle")}else{m.Values["edges_silenced"]=v}
	}
	if config.TraitSynthesis {
		v,err:=SynthesizeTraits(ctx,driver,llm,embedder,groupID,episodeUUIDs);if err!=nil{fail("traits")}else{m.Values["traits_synthesized"]=v}
	}
	if config.SelfAwareness {
		v,err:=RunSelfAwarenessPass(ctx,driver,llm,groupID,episodeUUIDs,config);if err!=nil{fail("self_awareness")}else{m.Values["self_model_updated"]=v}
	}
	if config.GoalSynthesis {
		v,err:=SynthesizeGoals(ctx,driver,llm,embedder,groupID,episodeUUIDs);if err!=nil{fail("goals")}else{m.Values["goals_synthesized"]=v}
	}
	if config.ProceduralSynthesis {
		v,err:=DetectInteractionPatterns(ctx,driver,embedder,groupID,episodeUUIDs);if err!=nil{fail("procedural")}else{m.Values["episodes_classified"]=v}
	}
	if config.Consolidation {
		v,err:=ConsolidateEdges(ctx,driver,embedder,groupID,config.ConsolidationThreshold,config.ConsolidationMinSpanDays);if err!=nil{fail("consolidation")}else{m.Values["edges_consolidated"]=v}
	}
	if config.Consolidation&&config.StagnantConsolidation {
		v,err:=ConsolidateStagnantEdges(ctx,driver,embedder,groupID,config.StagnantMinEdgesPerSummary,config.StagnantMaxEdgesPerPass);if err!=nil{fail("low_vitality_consolidation")}else{m.Values["low_vitality_consolidated"]=v}
	}
	if config.DomainLabelingEveryNPasses>0&&passCount%maxInt(1,config.DomainLabelingEveryNPasses)==0 {
		v,err:=LabelCommunityDomains(ctx,driver,llm,groupID);if err!=nil{fail("clustering")}else{m.Values["domains_labelled"]=v}
	}
	if config.Prediction {
		v,err:=SynthesizePrediction(ctx,driver,llm,groupID);if err!=nil{fail("prediction")}else if v!=nil{m.Values["prediction"]="ok"}else{m.Values["prediction"]="skipped"}
	}
	if len(episodeUUIDs)>0&&len(m.FailedSteps)==0 {
		_,err:=driver.Query(ctx,`
UPDATE episode SET
 cognition_processed_at = $processed_at,
 cognition_version = $version
WHERE group_id = $group_id
 AND uuid IN $episode_uuids;`,map[string]any{"group_id":groupID,"episode_uuids":episodeUUIDs,"processed_at":utcNow(),"version":CognitionVersion})
		if err!=nil{fail("mark_processed")}
	}
	m.Processed=len(m.FailedSteps)==0
	m.Values["processed"]=m.Processed
	m.Values["failed_steps"]=append([]string(nil),m.FailedSteps...)
	m.DurationMS=time.Since(start).Milliseconds()
	m.Values["duration_ms"]=m.DurationMS
	return m
}

func maxInt(a,b int)int{if a>b{return a};return b}
