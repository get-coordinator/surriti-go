package surriti

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"regexp"
	"strings"
)

var selfSlugRE=regexp.MustCompile("[^a-z0-9]+")

func selfSlug(value string)string{
	s:=strings.Trim(selfSlugRE.ReplaceAllString(strings.ToLower(value),"_"),"_")
	if s==""{s="self_model"};if len(s)>80{s=s[:80]};return s
}

func stableSelfID(prefix,groupID,value string)string{
	sum:=sha1.Sum([]byte(groupID+"\x00"+value))
	return prefix+"_"+selfSlug(value)+"_"+hex.EncodeToString(sum[:])[:12]
}

func querySelfEpisodes(ctx context.Context,driver Queryer,groupID string,episodeUUIDs []string)([]map[string]any,error){
	if len(episodeUUIDs)>0{
		raw,err:=driver.Query(ctx,`
SELECT name, content, source, source_description, reference_time, created_at, group_id
FROM episode
WHERE group_id = $group_id
 AND uuid IN $episode_uuids
 AND source CONTAINS 'self_'
ORDER BY created_at DESC
LIMIT 100;`,map[string]any{"group_id":groupID,"episode_uuids":episodeUUIDs})
		if err!=nil{return nil,err};rows:=UnwrapRows(raw);if len(rows)>0{return rows,nil}
	}
	raw,err:=driver.Query(ctx,`
SELECT name, content, source, source_description, reference_time, created_at, group_id
FROM episode
WHERE group_id = $group_id
 AND source CONTAINS 'self_'
ORDER BY created_at DESC
LIMIT 100;`,map[string]any{"group_id":groupID})
	if err!=nil{return nil,err};return UnwrapRows(raw),nil
}

func getSelfEntityRow(ctx context.Context,driver Queryer,groupID string)(map[string]any,error){
	name:="assistant";if groupID!=""{name="assistant_"+groupID}
	raw,err:=driver.Query(ctx,"SELECT * FROM entity WHERE group_id = $group_id AND name = $name LIMIT 1;",map[string]any{"group_id":groupID,"name":name})
	if err!=nil{return nil,err};rows:=UnwrapRows(raw);if len(rows)==0{return nil,nil};return rows[0],nil
}

func upsertSelfModelEntity(ctx context.Context,driver Queryer,uuid,groupID,name,summary string,labels []string)(string,error){
	raw,err:=driver.Query(ctx,"SELECT * FROM entity WHERE group_id = $group_id AND uuid = $uuid LIMIT 1;",map[string]any{"group_id":groupID,"uuid":uuid});if err!=nil{return "",err}
	payload:=map[string]any{"uuid":uuid,"group_id":groupID,"name":name,"summary":summary,"labels":labels,"created_at":utcNow()}
	if len(UnwrapRows(raw))>0{
		_,err=driver.Query(ctx,`UPDATE type::record("entity", $uuid) SET summary = $summary, labels = $labels;`,payload);return uuid,err
	}
	_,err=driver.Query(ctx,`
CREATE type::record("entity", $uuid) CONTENT {
 uuid: $uuid, group_id: $group_id, name: $name, summary: $summary,
 labels: $labels, attributes: {}, created_at: $created_at
};`,payload)
	if err==nil{return uuid,nil}
	if !strings.Contains(err.Error(),"entity_name_uniq"){return "",err}
	fallback,qerr:=driver.Query(ctx,"SELECT * FROM entity WHERE group_id = $group_id AND name = $name LIMIT 1;",map[string]any{"group_id":groupID,"name":name});if qerr!=nil{return "",qerr}
	rows:=UnwrapRows(fallback);if len(rows)==0{return "",err};actual:=stringFromAny(rows[0]["uuid"]);payload["uuid"]=actual
	_,qerr=driver.Query(ctx,`UPDATE type::record("entity", $uuid) SET summary = $summary, labels = $labels;`,payload);return actual,qerr
}

func upsertSelfModelEdge(ctx context.Context,driver Queryer,groupID,selfUUID,targetUUID,edgeUUID,predicate,fact string,confidence float64,isBelief bool)error{
	raw,err:=driver.Query(ctx,"SELECT * FROM relates_to WHERE group_id = $group_id AND uuid = $uuid LIMIT 1;",map[string]any{"group_id":groupID,"uuid":edgeUUID});if err!=nil{return err}
	payload:=map[string]any{"src":selfUUID,"tgt":targetUUID,"uuid":edgeUUID,"group_id":groupID,"name":predicate,"fact":fact,"confidence":confidence,"is_belief":isBelief,"status":"active","source_type":"assistant","attributes":map[string]any{"memory_class":"self_model"},"fact_key":MakeFactKey(groupID,selfUUID,predicate,targetUUID,""),"created_at":utcNow()}
	if len(UnwrapRows(raw))>0{
		_,err=driver.Query(ctx,`
UPDATE relates_to SET fact=$fact, confidence=$confidence, is_belief=$is_belief,
 status="active", invalid_at=NONE, fact_key=$fact_key, attributes=$attributes
WHERE group_id=$group_id AND uuid=$uuid;`,payload);return err
	}
	_,err=driver.Query(ctx,`
RELATE (type::record("entity", $src))->relates_to->(type::record("entity", $tgt))
CONTENT {
 uuid:$uuid, group_id:$group_id, name:$name, fact:$fact,
 confidence:$confidence, is_belief:$is_belief, status:$status,
 source_type:$source_type, fact_key:$fact_key, attributes:$attributes,
 episodes:[], reinforcement_count:1, recall_count:0, decay_score:1.0,
 stability:"persistent", created_at:$created_at
};`,payload);return err
}

func renderSelfEpisodes(episodes []map[string]any)string{
	var b strings.Builder
	n:=len(episodes);if n>12{n=12}
	for i:=0;i<n;i++{
		ep:=episodes[i];src:=stringFromAny(ep["source_description"]);if src==""{src=stringFromAny(ep["source"])}
		content:=stringFromAny(ep["content"]);if len(content)>600{content=content[:600]+"…"}
		b.WriteString("["+src+"] "+content+"\n\n")
	}
	return b.String()
}

func extractSelfModelItems(ctx context.Context,llm LLMClient,episodes []map[string]any,kind string)(map[string]any,error){
	synth,ok:=llm.(Synthesizer);if !ok{return nil,nil}
	system:="Extract structured self-model data from AI self-observations. Return only valid JSON."
	prompt:="Analyze these AI assistant self-observations and return JSON with traits and beliefs. Traits/beliefs need confidence >= 0.5.\n\n"+renderSelfEpisodes(episodes)
	if kind=="pattern"{system="Extract recurring behavioral patterns. Return only valid JSON.";prompt="Identify recurring behavioral patterns and return JSON with a patterns array containing pattern, frequency, context.\n\n"+renderSelfEpisodes(episodes)}
	raw,err:=synth.Synthesize(ctx,system,prompt);if err!=nil||raw==""{return nil,err}
	parsed,ok:=ParseJSONLoose(raw).(map[string]any);if !ok{return nil,nil};return parsed,nil
}

func writeSelfTrait(ctx context.Context,driver Queryer,groupID string,self map[string]any,item map[string]any)(bool,error){
	name:=stringFromAny(item["trait"]);if name==""{return false,nil};conf:=.5;if v,ok:=toFloat(item["confidence"]);ok{conf=v};if conf<.5{return false,nil}
	u:=stableSelfID("trait",groupID,name);evidence:=stringFromAny(item["evidence"]);summary:=evidence;if summary==""{summary="Self-trait: "+name}
	actual,err:=upsertSelfModelEntity(ctx,driver,u,groupID,name,summary,[]string{"SelfTrait","Trait"});if err!=nil{return false,err}
	err=upsertSelfModelEdge(ctx,driver,groupID,stringFromAny(self["uuid"]),actual,"edge_"+actual,"has_trait","has_trait: "+name,conf,false);return err==nil,err
}

func writeSelfBelief(ctx context.Context,driver Queryer,groupID string,self map[string]any,item map[string]any)(bool,error){
	text:=stringFromAny(item["belief"]);if text==""{return false,nil};conf:=.5;if v,ok:=toFloat(item["confidence"]);ok{conf=v};if conf<.5{return false,nil}
	u:=stableSelfID("belief",groupID,text);summary:=text;if ev:=stringFromAny(item["evidence"]);ev!=""{if len(ev)>200{ev=ev[:200]};summary=text+" [evidence: "+ev+"]"}
	actual,err:=upsertSelfModelEntity(ctx,driver,u,groupID,"self_belief",summary,[]string{"SelfBelief"});if err!=nil{return false,err}
	err=upsertSelfModelEdge(ctx,driver,groupID,stringFromAny(self["uuid"]),actual,"edge_"+actual,"has_belief",text,conf,true);return err==nil,err
}

func writeSelfPattern(ctx context.Context,driver Queryer,groupID string,self map[string]any,item map[string]any)(bool,error){
	name:=stringFromAny(item["pattern"]);if name==""{return false,nil};parts:=[]string{name};if v:=stringFromAny(item["frequency"]);v!=""{parts=append(parts,"frequency="+v)};if v:=stringFromAny(item["context"]);v!=""{parts=append(parts,"context="+v)}
	u:=stableSelfID("pattern",groupID,name);actual,err:=upsertSelfModelEntity(ctx,driver,u,groupID,name,strings.Join(parts," | "),[]string{"SelfPattern","Pattern"});if err!=nil{return false,err}
	err=upsertSelfModelEdge(ctx,driver,groupID,stringFromAny(self["uuid"]),actual,"edge_"+actual,"has_pattern","has_pattern: "+name,.7,false);return err==nil,err
}

func RunSelfAwarenessPass(ctx context.Context,driver Queryer,llm LLMClient,groupID string,episodeUUIDs []string,config CognitionConfig)(map[string]int,error){
	metrics:=map[string]int{"self_episodes_read":0,"self_traits_extracted":0,"self_beliefs_extracted":0,"self_patterns_detected":0}
	episodes,err:=querySelfEpisodes(ctx,driver,groupID,episodeUUIDs);if err!=nil{return metrics,err};metrics["self_episodes_read"]=len(episodes);if len(episodes)==0{return metrics,nil}
	self,err:=getSelfEntityRow(ctx,driver,groupID);if err!=nil{return metrics,err};if self==nil{return metrics,nil}
	byType:=map[string][]map[string]any{};for _,ep:=range episodes{src:=stringFromAny(ep["source"]);if strings.HasPrefix(src,"self_"){byType[src]=append(byType[src],ep)}}
	for typ,eps:=range byType{
		if typ==string(EpisodeSelfPattern){
			data,e:=extractSelfModelItems(ctx,llm,eps,"pattern");if e!=nil{continue};for _,raw:=range asAnySlice(data["patterns"]){m:=mapFromAny(raw);ok,e:=writeSelfPattern(ctx,driver,groupID,self,m);if e==nil&&ok{metrics["self_patterns_detected"]++}}
			continue
		}
		if typ!=string(EpisodeSelfObservation)&&typ!=string(EpisodeSelfCorrection)&&typ!=string(EpisodeSelfSuccess){continue}
		data,e:=extractSelfModelItems(ctx,llm,eps,"trait");if e!=nil{continue}
		for _,raw:=range asAnySlice(data["traits"]){m:=mapFromAny(raw);ok,e:=writeSelfTrait(ctx,driver,groupID,self,m);if e==nil&&ok{metrics["self_traits_extracted"]++}}
		for _,raw:=range asAnySlice(data["beliefs"]){m:=mapFromAny(raw);ok,e:=writeSelfBelief(ctx,driver,groupID,self,m);if e==nil&&ok{metrics["self_beliefs_extracted"]++}}
	}
	return metrics,nil
}
