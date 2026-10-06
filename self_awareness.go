package surriti

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

func (s *Surriti) getEntityByName(ctx context.Context,name,groupID string)(*EntityNode,error){
	raw,err:=s.Driver.Query(ctx,`
SELECT * FROM entity
WHERE group_id = $group_id AND name = $name
LIMIT 1;`,map[string]any{"group_id":groupID,"name":name})
	if err!=nil{return nil,err};rows:=UnwrapRows(raw);if len(rows)==0{return nil,nil};n:=ParseEntity(rows[0]);return &n,nil
}

func (s *Surriti) AddSelfEpisode(ctx context.Context,episodeType EpisodeType,content any,groupID,name string,referenceTime *time.Time,sourceDescription string)(AddEpisodeResults,error){
	switch episodeType{
	case EpisodeSelfObservation,EpisodeSelfCorrection,EpisodeSelfSuccess,EpisodeSelfPattern:
	default:return AddEpisodeResults{},fmt.Errorf("%w: invalid self episode type %q",ErrConfig,episodeType)
	}
	var text string
	switch v:=content.(type){
	case string:text=v
	default:
		b,err:=json.Marshal(v);if err!=nil{return AddEpisodeResults{},err};text=string(b)
	}
	if name==""{name="self_"+string(episodeType)}
	if sourceDescription==""{sourceDescription="self_"+string(episodeType)}
	ref:=utcNow();if referenceTime!=nil{ref=referenceTime.UTC()}
	episode:=NewEpisodicNode(name,groupID);episode.Content=text;episode.Source=episodeType;episode.SourceDescription=sourceDescription;episode.ReferenceTime=ref
	if err:=s.saveEpisode(ctx,episode);err!=nil{return AddEpisodeResults{},err}
	extraction,err:=s.LLM.Extract(ctx,ExtractionRequest{
		Content:text,GroupID:groupID,
		CustomInstructions:"This is a SELF-REFERENTIAL episode about the AI assistant's own behavior, not about the user or the world. Extract facts about the assistant's behavior, patterns, or self-assessment. Do NOT extract facts about external entities or world knowledge.",
	})
	if err!=nil{return AddEpisodeResults{},err}
	selfName:="assistant";if groupID!=""{selfName="assistant_"+groupID}
	inputs:=[]ExtractedEntity{{Name:selfName,Summary:"Self-referential entity for group "+func()string{if groupID==""{return "default"};return groupID}(),Labels:[]string{"SelfEntity","Assistant"}}}
	inputs=append(inputs,extraction.Entities...)
	for _,fact:=range extraction.Facts{if fact.Object!=""{inputs=append(inputs,ExtractedEntity{Name:fact.Object,Labels:[]string{"Entity"}})}}
	entities,err:=s.upsertEntities(ctx,inputs,groupID,&episode.UUID,text);if err!=nil{return AddEpisodeResults{},err}
	byKey:=map[string]EntityNode{};for _,e:=range entities{byKey[entityNameKey(e.Name)]=e}
	selfEntity,ok:=byKey[entityNameKey(selfName)];if !ok{return AddEpisodeResults{},fmt.Errorf("%w: self entity not persisted",ErrNotFound)}
	edges:=[]EntityEdge{}
	for _,fact:=range extraction.Facts{
		obj,ok:=byKey[entityNameKey(fact.Object)];if !ok{continue}
		fact.Subject=selfName
		edge,invalidated,err:=s.addFactEdge(ctx,fact,selfEntity,obj,&episode,groupID,"assistant");if err!=nil{return AddEpisodeResults{},err}
		edges=append(edges,edge);edges=append(edges,invalidated...)
	}
	mentions,err:=s.linkMentions(ctx,episode,[]EntityNode{selfEntity},groupID);if err!=nil{return AddEpisodeResults{},err}
	return AddEpisodeResults{Episode:episode,EpisodicEdges:mentions,Nodes:entities,Edges:edges,InvalidatedEdges:[]EntityEdge{},Communities:[]CommunityNode{},CommunityEdges:[]CommunityEdge{}},nil
}

func (s *Surriti) GetSelfModel(ctx context.Context,groupID string)(map[string]any,error){
	result:=map[string]any{"group_id":groupID,"traits":[]map[string]any{},"patterns":[]map[string]any{},"beliefs":[]map[string]any{},"goals":[]map[string]any{},"summary":""}
	selfName:="assistant";if groupID!=""{selfName="assistant_"+groupID}
	selfEntity,err:=s.getEntityByName(ctx,selfName,groupID);if err!=nil{return nil,err}

	traits:=[]map[string]any{}
	if selfEntity!=nil{
		raw,err:=s.Driver.Query(ctx,`
SELECT * FROM relates_to
WHERE group_id = $group_id
 AND in = type::record("entity", $src)
 AND name = "has_trait"
 AND status = "active"
 AND invalid_at IS NONE;`,map[string]any{"group_id":groupID,"src":selfEntity.UUID})
		if err!=nil{return nil,err}
		for _,row:=range UnwrapRows(raw){conf:=1.0;if v,ok:=toFloat(row["confidence"]);ok{conf=v};traits=append(traits,map[string]any{"fact":stringFromAny(row["fact"]),"confidence":conf})}
	}
	result["traits"]=traits

	rawEpisodes,err:=s.Driver.Query(ctx,`
SELECT name, content, interaction_pattern, created_at
FROM episode
WHERE group_id = $group_id
 AND source CONTAINS 'self_'
ORDER BY created_at DESC
LIMIT 50;`,map[string]any{"group_id":groupID})
	if err!=nil{return nil,err}
	epRows:=UnwrapRows(rawEpisodes);patterns:=map[string]map[string]any{}
	for _,row:=range epRows{
		p:=stringFromAny(row["interaction_pattern"]);if p==""{continue}
		entry:=patterns[p];if entry==nil{entry=map[string]any{"pattern":p,"count":0,"source":"episode"};patterns[p]=entry}
		entry["count"]=intFromAny(entry["count"])+1
	}
	if selfEntity!=nil{
		raw,err:=s.Driver.Query(ctx,`
SELECT * FROM relates_to
WHERE group_id = $group_id
 AND in = type::record("entity", $src)
 AND name = "has_pattern"
 AND status = "active"
 AND invalid_at IS NONE;`,map[string]any{"group_id":groupID,"src":selfEntity.UUID})
		if err!=nil{return nil,err}
		for _,row:=range UnwrapRows(raw){
			fact:=stringFromAny(row["fact"]);p:=strings.TrimSpace(strings.TrimPrefix(fact,"has_pattern:"));if p==""{p=fact};if p==""{continue}
			conf:=1.0;if v,ok:=toFloat(row["confidence"]);ok{conf=v}
			patterns[p]=map[string]any{"pattern":p,"count":1,"confidence":conf,"source":"self_model"}
		}
	}
	patternList:=make([]map[string]any,0,len(patterns));for _,entry:=range patterns{
		if _,ok:=entry["confidence"];!ok{den:=len(epRows);if den<1{den=1};entry["confidence"]=float64(intFromAny(entry["count"]))/float64(den)}
		patternList=append(patternList,entry)
	}
	sort.SliceStable(patternList,func(i,j int)bool{return intFromAny(patternList[i]["count"])>intFromAny(patternList[j]["count"])})
	result["patterns"]=patternList

	beliefs:=[]map[string]any{}
	if selfEntity!=nil{
		raw,err:=s.Driver.Query(ctx,`
SELECT * FROM relates_to
WHERE group_id = $group_id
 AND in = type::record("entity", $src)
 AND name = "has_belief"
 AND is_belief = true
 AND status = "active"
 AND invalid_at IS NONE;`,map[string]any{"group_id":groupID,"src":selfEntity.UUID})
		if err!=nil{return nil,err}
		for _,row:=range UnwrapRows(raw){beliefs=append(beliefs,map[string]any{"fact":stringFromAny(row["fact"]),"source_type":"self"})}
	}
	result["beliefs"]=beliefs
	countRaw,err:=s.Driver.Query(ctx,`
SELECT count() as cnt FROM episode
WHERE group_id = $group_id AND source CONTAINS 'self_';`,map[string]any{"group_id":groupID})
	if err!=nil{return nil,err};count:=0;rows:=UnwrapRows(countRaw);if len(rows)>0{count=intFromAny(rows[0]["cnt"])}
	result["summary"]=fmt.Sprintf("Self-model based on %d self-episodes. %d traits identified. %d interaction patterns detected.",count,len(traits),len(patternList))
	return result,nil
}
