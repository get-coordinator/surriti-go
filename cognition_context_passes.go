package surriti

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

var optimizationRE=regexp.MustCompile(`(?i)\b(?:best|optimal|optimi[sz]e|improve|better|tune|refine|tweak|sharper|tighter)\b`)
var procTokenRE=regexp.MustCompile("[a-zA-Z']+")

func procTokens(text string)map[string]struct{}{
	out:=map[string]struct{}{}
	for _,t:=range procTokenRE.FindAllString(strings.ToLower(text),-1){if len(t)>3{out[t]=struct{}{}}}
	return out
}

func ClassifyEpisode(text,prior string)string{
	if text==""{return "one_off_query"}
	if optimizationRE.MatchString(text){return "optimization_request"}
	qs:=strings.Count(text,"?");words:=len(strings.Fields(text))
	if qs==0&&words>=60{return "narrative_share"}
	if prior!=""{
		a,b:=procTokens(text),procTokens(prior);overlap:=0
		for t:=range a{if _,ok:=b[t];ok{overlap++}}
		if overlap>=3&&qs>=1{return "iterative_refinement"}
		if overlap>=2&&words<40{return "clarification"}
	}
	return "one_off_query"
}

func DetectInteractionPatterns(ctx context.Context,driver Queryer,embedder Embedder,groupID string,episodeUUIDs []string)(int,error){
	if len(episodeUUIDs)==0{return 0,nil}
	raw,err:=driver.Query(ctx,`SELECT uuid, content, reference_time FROM episode WHERE group_id = $g ORDER BY reference_time DESC LIMIT $n;`,map[string]any{"g":groupID,"n":8});if err!=nil{return 0,err}
	rows:=UnwrapRows(raw)
	sort.SliceStable(rows,func(i,j int)bool{
		a,b:=coerceTime(rows[i]["reference_time"]),coerceTime(rows[j]["reference_time"])
		if a!=nil&&b!=nil{return a.Before(*b)}
		return stringFromAny(rows[i]["reference_time"])<stringFromAny(rows[j]["reference_time"])
	})
	target:=map[string]struct{}{};for _,u:=range episodeUUIDs{target[u]=struct{}{}}
	counts:=map[string]int{};classified:=0;prior:=""
	for _,r:=range rows{
		text:=stringFromAny(r["content"]);label:=ClassifyEpisode(text,prior)
		if _,ok:=target[stringFromAny(r["uuid"])];ok{
			if _,err:=driver.Query(ctx,"UPDATE episode SET interaction_pattern = $p WHERE uuid = $u;",map[string]any{"u":r["uuid"],"p":label});err!=nil{return classified,err};classified++
		}
		counts[label]++;prior=text
	}
	dominant:="";hits:=0
	for label,n:=range counts{if n>hits{dominant=label;hits=n}}
	if hits<5||dominant=="one_off_query"{return classified,nil}
	raw,err=driver.Query(ctx,`SELECT record::id(out) AS entity_uuid, count() AS n FROM mentions WHERE group_id = $g GROUP BY entity_uuid ORDER BY n DESC LIMIT 1;`,map[string]any{"g":groupID});if err!=nil{return classified,err}
	speakers:=UnwrapRows(raw);if len(speakers)==0{return classified,nil}
	speaker:=stringFromAny(speakers[0]["entity_uuid"]);if speaker==""{return classified,nil}
	now:=utcNow();patternUUID,err:=UpsertSyntheticEntity(ctx,driver,groupID,dominant,"interaction pattern: "+dominant,"pattern",now);if err!=nil{return classified,err}
	_,err=UpsertSyntheticEdge(ctx,driver,embedder,groupID,speaker,patternUUID,"interaction_style","interacts via "+strings.ReplaceAll(dominant,"_"," "),"procedural",math.Min(1,.5+.06*float64(hits)),nil,nil,"reinforced",false,nil,nil,"",now)
	return classified,err
}

var domainTokenRE=regexp.MustCompile("[a-zA-Z]{4,}")
var domainStop=map[string]struct{}{
	"have":{},"with":{},"this":{},"that":{},"from":{},"user":{},"also":{},"into":{},"their":{},"there":{},"about":{},"they":{},"your":{},"more":{},"much":{},"some":{},"what":{},"when":{},"would":{},"could":{},"should":{},"really":{},"actually":{},
}

func topTerms(texts []string,k int)[]string{
	counts:=map[string]int{};order:=map[string]int{};next:=0
	for _,t:=range texts{for _,tok:=range domainTokenRE.FindAllString(strings.ToLower(t),-1){if _,stop:=domainStop[tok];stop{continue};if _,seen:=order[tok];!seen{order[tok]=next;next++};counts[tok]++}}
	type kv struct{s string;n int;order int};vals:=[]kv{};for s,n:=range counts{vals=append(vals,kv{s,n,order[s]})}
	sort.SliceStable(vals,func(i,j int)bool{if vals[i].n!=vals[j].n{return vals[i].n>vals[j].n};return vals[i].order<vals[j].order})
	if k>len(vals){k=len(vals)};out:=make([]string,k);for i:=0;i<k;i++{out[i]=vals[i].s};return out
}

func LabelCommunityDomains(ctx context.Context,driver Queryer,llm LLMClient,groupID string)(int,error){
	raw,err:=driver.Query(ctx,"SELECT uuid, name FROM community WHERE group_id = $g AND kind = 'cluster';",map[string]any{"g":groupID});if err!=nil{return 0,err}
	labelled:=0
	for _,c:=range UnwrapRows(raw){
		cid:=stringFromAny(c["uuid"])
		membersRaw,err:=driver.Query(ctx,`SELECT record::id(out) AS entity_uuid FROM has_member WHERE group_id = $g AND record::id(in) = $c;`,map[string]any{"g":groupID,"c":cid});if err!=nil{return labelled,err}
		ids:=[]string{};for _,m:=range UnwrapRows(membersRaw){if id:=stringFromAny(m["entity_uuid"]);id!=""{ids=append(ids,id)}};if len(ids)==0{continue}
		entsRaw,err:=driver.Query(ctx,"SELECT name, summary FROM entity WHERE uuid IN $u;",map[string]any{"u":ids});if err!=nil{return labelled,err}
		edgeRaw,err:=driver.Query(ctx,`SELECT fact FROM relates_to WHERE group_id = $g AND (record::id(in) IN $u OR record::id(out) IN $u) AND status = 'active' LIMIT 24;`,map[string]any{"g":groupID,"u":ids});if err!=nil{return labelled,err}
		names,facts:=[]string{},[]string{};for _,r:=range UnwrapRows(entsRaw){names=append(names,stringFromAny(r["name"]))};for _,r:=range UnwrapRows(edgeRaw){facts=append(facts,stringFromAny(r["fact"]))}
		terms:=topTerms(append(append([]string{},names...),facts...),8);if len(terms)==0{continue};label:=""
		if synth,ok:=llm.(Synthesizer);ok{
			nameLimit:=len(names);if nameLimit>12{nameLimit=12};factLimit:=len(facts);if factLimit>8{factLimit=8}
			factLines:=make([]string,0,factLimit);for _,fact:=range facts[:factLimit]{factLines=append(factLines,"- "+fact)}
			user:="CLUSTER ENTITIES: "+strings.Join(names[:nameLimit],", ")+"\nCLUSTER FACTS:\n"+strings.Join(factLines,"\n")+"\nTOP TOKENS: "+strings.Join(terms,", ")
			if raw,e:=synth.Synthesize(ctx,DomainLabelSystem,user);e==nil&&raw!=""{label=SnakeCase(strings.Trim(strings.Split(raw,"\n")[0],"\"'` "))}
		}
		if label==""{label=SnakeCase(terms[0])};if label==""{continue}
		if _,err:=driver.Query(ctx,"UPDATE community SET domain = $d WHERE uuid = $u;",map[string]any{"u":cid,"d":label});err!=nil{return labelled,err}
		if _,err:=driver.Query(ctx,"UPDATE entity SET domain = $d WHERE uuid IN $u;",map[string]any{"u":ids,"d":label});err!=nil{return labelled,err}
		labelled++
	}
	return labelled,nil
}

func pyReprString(value string) string {
	value=strings.ReplaceAll(value,"\\","\\\\")
	value=strings.ReplaceAll(value,"'","\\'")
	return "'"+value+"'"
}

func pyReprStrings(values []string) string {
	parts:=make([]string,len(values));for i,v:=range values{parts[i]=pyReprString(v)}
	return "["+strings.Join(parts,", ")+"]"
}

func pyReprGoals(values []map[string]string) string {
	parts:=make([]string,len(values))
	for i,v:=range values{parts[i]="{'name': "+pyReprString(v["name"])+", 'summary': "+pyReprString(v["summary"])+"}"}
	return "["+strings.Join(parts,", ")+"]"
}

func SynthesizePrediction(ctx context.Context,driver Queryer,llm LLMClient,groupID string)(map[string]any,error){
	goalsRaw,err:=driver.Query(ctx,`SELECT name, summary FROM entity WHERE group_id = $g AND 'goal' IN labels LIMIT 10;`,map[string]any{"g":groupID});if err!=nil{return nil,err}
	goals:=[]map[string]string{};for _,r:=range UnwrapRows(goalsRaw){goals=append(goals,map[string]string{"name":stringFromAny(r["name"]),"summary":stringFromAny(r["summary"])})}

	domainRaw,err:=driver.Query(ctx,"SELECT domain FROM entity WHERE group_id = $g AND domain IS NOT NONE;",map[string]any{"g":groupID});if err!=nil{return nil,err}
	dc:=map[string]int{};domainOrder:=map[string]int{};nextDomain:=0
	for _,r:=range UnwrapRows(domainRaw){if d:=stringFromAny(r["domain"]);d!=""{if _,ok:=domainOrder[d];!ok{domainOrder[d]=nextDomain;nextDomain++};dc[d]++}}
	type domainKV struct{s string;n int;order int};dv:=[]domainKV{};for d,n:=range dc{dv=append(dv,domainKV{d,n,domainOrder[d]})}
	sort.SliceStable(dv,func(i,j int)bool{if dv[i].n!=dv[j].n{return dv[i].n>dv[j].n};return dv[i].order<dv[j].order})
	domains:=[]string{};for i,v:=range dv{if i>=5{break};domains=append(domains,v.s)}

	patRaw,err:=driver.Query(ctx,`SELECT interaction_pattern, reference_time FROM episode WHERE group_id = $g AND interaction_pattern IS NOT NONE ORDER BY reference_time DESC LIMIT 8;`,map[string]any{"g":groupID});if err!=nil{return nil,err}
	pc:=map[string]int{};patternOrder:=map[string]int{};nextPattern:=0
	for _,r:=range UnwrapRows(patRaw){if p:=stringFromAny(r["interaction_pattern"]);p!=""{if _,ok:=patternOrder[p];!ok{patternOrder[p]=nextPattern;nextPattern++};pc[p]++}}
	dominant:="";nmax:=0;bestOrder:=int(^uint(0)>>1)
	for p,n:=range pc{ord:=patternOrder[p];if n>nmax||(n==nmax&&ord<bestOrder){dominant=p;nmax=n;bestOrder=ord}}
	if len(goals)==0&&len(domains)==0&&dominant==""{return nil,nil}

	dominantRepr:="None";if dominant!=""{dominantRepr=pyReprString(dominant)}
	user:="ACTIVE_GOALS: "+pyReprGoals(goals)+"\nDOMINANT_DOMAINS: "+pyReprStrings(domains)+"\nDOMINANT_INTERACTION_PATTERN: "+dominantRepr
	var bundle map[string]any
	if synth,ok:=llm.(Synthesizer);ok{
		if raw,e:=synth.Synthesize(ctx,PredictionSystem,user);e==nil{
			if parsed,ok:=ParseJSONLoose(raw).(map[string]any);ok{
				bundle=map[string]any{}
				for _,key:=range []string{"likely_next_topics","likely_preferences","likely_questions"}{vals:=asStringSlice(parsed[key]);if len(vals)>5{vals=vals[:5]};bundle[key]=vals}
			}
		}
	}
	if bundle==nil{
		prefs:=[]string{};for _,g:=range goals{prefs=append(prefs,g["name"]);if len(prefs)>=3{break}}
		topics:=append([]string(nil),domains...);if len(topics)>3{topics=topics[:3]}
		bundle=map[string]any{"likely_next_topics":topics,"likely_preferences":prefs,"likely_questions":[]string{}}
	}
	if dominant==""{bundle["dominant_pattern"]=nil}else{bundle["dominant_pattern"]=dominant};bundle["refreshed_at"]=utcNow().Format(time.RFC3339Nano)
	raw,err:=driver.Query(ctx,"SELECT uuid FROM community WHERE group_id = $g AND kind = 'prediction' LIMIT 1;",map[string]any{"g":groupID});if err!=nil{return nil,err};rows:=UnwrapRows(raw)
	if len(rows)>0{_,err=driver.Query(ctx,"UPDATE community SET payload = $p WHERE uuid = $u;",map[string]any{"u":rows[0]["uuid"],"p":bundle})}else{_,err=driver.Query(ctx,`CREATE community CONTENT { uuid: $u, group_id: $g, name: 'prediction', kind: 'prediction', summary: '', payload: $p, created_at: time::now() };`,map[string]any{"u":newUUID(),"g":groupID,"p":bundle})}
	return bundle,err
}
