package surriti

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	minTraitReinforcement=2
	maxTraitCandidatesPerSubject=12
)

var nonTraitPredicates=map[string]struct{}{
	"has_trait":{},"pursues_goal":{},"is_age":{},"has_birthday":{},"has_age":{},"is_a":{},
	"is":{},"is_named":{},"name_is":{},"called":{},"aka":{},
}

type traitCandidate struct {
	Kind string
	Predicate string
	ObjectUUID string
	Reinforcement int
	Supporting []EntityEdge
}

func loadCognitionEdges(ctx context.Context,driver Queryer,groupID string)([]EntityEdge,error){
	raw,err:=driver.Query(ctx,`
SELECT *, record::id(in) AS source_node_uuid, record::id(out) AS target_node_uuid
FROM relates_to WHERE group_id = $g;`,map[string]any{"g":groupID})
	if err!=nil{return nil,err}
	out:=[]EntityEdge{};for _,r:=range UnwrapRows(raw){out=append(out,ParseEdge(r))};return out,nil
}

func traitCandidatesForSubject(subject string,edges []EntityEdge)[]traitCandidate{
	pairs:=map[string][]EntityEdge{};preds:=map[string][]EntityEdge{}
	for _,e:=range edges{
		if e.SourceNodeUUID!=subject||e.Status!="active"||e.IsBelief||e.Singleton{continue}
		switch e.MemoryClass{case "objective","preference","style","trait","sentiment":default:continue}
		p:=strings.ToLower(strings.TrimSpace(e.CanonicalName));if p==""{p=strings.ToLower(strings.TrimSpace(e.Name))}
		if p==""{continue};if _,skip:=nonTraitPredicates[p];skip{continue}
		pairs[p+"\x00"+e.TargetNodeUUID]=append(pairs[p+"\x00"+e.TargetNodeUUID],e);preds[p]=append(preds[p],e)
	}
	out:=[]traitCandidate{}
	for key,bucket:=range pairs{
		reinforce:=0;for _,e:=range bucket{n:=e.ReinforcementCount;if n<1{n=1};reinforce+=n}
		if reinforce<minTraitReinforcement&&len(bucket)<minTraitReinforcement{continue}
		parts:=strings.SplitN(key,"\x00",2);out=append(out,traitCandidate{Kind:"pair",Predicate:parts[0],ObjectUUID:parts[1],Reinforcement:reinforce,Supporting:bucket})
	}
	for p,bucket:=range preds{
		if len(bucket)<minTraitReinforcement+1{continue};reinforce:=0;for _,e:=range bucket{n:=e.ReinforcementCount;if n<1{n=1};reinforce+=n}
		out=append(out,traitCandidate{Kind:"predicate",Predicate:p,Reinforcement:reinforce,Supporting:bucket})
	}
	sort.SliceStable(out,func(i,j int)bool{return out[i].Reinforcement>out[j].Reinforcement})
	if len(out)>maxTraitCandidatesPerSubject{out=out[:maxTraitCandidatesPerSubject]};return out
}

func ratifyTraitCandidates(ctx context.Context,llm LLMClient,candidates []traitCandidate)[]map[string]any{
	var b strings.Builder;b.WriteString("CANDIDATE TRAITS:\n")
	for i,c:=range candidates{
		fmt.Fprintf(&b,"[%d] (%s) predicate=%q reinforcement=%d\n",i,c.Kind,c.Predicate,c.Reinforcement)
		n:=len(c.Supporting);if n>4{n=4};for _,e:=range c.Supporting[:n]{fact:=e.Fact;if fact==""{fact=e.Name};b.WriteString("  - "+fact+"\n")}
	}
	if synth,ok:=llm.(Synthesizer);ok{
		if raw,err:=synth.Synthesize(ctx,"Return a JSON array of durable traits with name, description, confidence, supporting_indices.",b.String());err==nil{
			if parsed,ok:=ParseJSONLoose(raw).([]any);ok&&len(parsed)>0{
				out:=[]map[string]any{}
				for _,item:=range parsed{
					m:=mapFromAny(item);if m==nil{continue};name:=SnakeCase(stringFromAny(m["name"]));if name=="unknown"{continue}
					conf:=.6;if v,ok:=toFloat(m["confidence"]);ok{conf=v};if conf<0{conf=0};if conf>1{conf=1}
					support:=[]EntityEdge{}
					for _,idxRaw:=range asAnySlice(m["supporting_indices"]){idx:=-1;switch x:=idxRaw.(type){case string:idx,_=strconv.Atoi(x);default:idx=intFromAny(x)};if idx>=0&&idx<len(candidates){support=append(support,candidates[idx].Supporting...)}}
					if len(support)==0&&len(candidates)>0{support=candidates[0].Supporting}
					out=append(out,map[string]any{"name":name,"description":stringFromAny(m["description"]),"confidence":conf,"supporting":support})
				}
				if len(out)>0{return out}
			}
		}
	}
	n:=len(candidates);if n>3{n=3};out:=[]map[string]any{}
	for _,c:=range candidates[:n]{name:=SnakeCase(c.Predicate);if c.Kind=="predicate"{name=SnakeCase(c.Predicate+"_pattern")};conf:=.4+.05*float64(c.Reinforcement);if conf>.95{conf=.95};out=append(out,map[string]any{"name":name,"description":"","confidence":conf,"supporting":c.Supporting})}
	return out
}

func SynthesizeTraits(ctx context.Context,driver Queryer,llm LLMClient,embedder Embedder,groupID string,episodeUUIDs []string)(int,error){
	if len(episodeUUIDs)==0{return 0,nil};edges,err:=loadCognitionEdges(ctx,driver,groupID);if err!=nil{return 0,err};if len(edges)==0{return 0,nil}
	epSet:=map[string]struct{}{};for _,u:=range episodeUUIDs{epSet[u]=struct{}{}};subjects:=map[string]struct{}{}
	for _,e:=range edges{for _,ep:=range e.Episodes{if _,ok:=epSet[ep];ok{subjects[e.SourceNodeUUID]=struct{}{};break}}}
	written:=0;now:=utcNow()
	for subject:=range subjects{
		cands:=traitCandidatesForSubject(subject,edges);if len(cands)==0{continue}
		for _,trait:=range ratifyTraitCandidates(ctx,llm,cands){
			name:=stringFromAny(trait["name"]);description:=stringFromAny(trait["description"]);conf,_:=toFloat(trait["confidence"])
			support,_:=trait["supporting"].([]EntityEdge);supportIDs:=[]string{};for _,e:=range support{supportIDs=append(supportIDs,e.UUID)}
			u,err:=UpsertSyntheticEntity(ctx,driver,groupID,name,description,"trait",now);if err!=nil{return written,err}
			fact:=description;if fact==""{fact="has trait: "+name}
			if _,err:=UpsertSyntheticEdge(ctx,driver,embedder,groupID,subject,u,"has_trait",fact,"trait",conf,supportIDs,nil,"reinforced",false,nil,nil,"",now);err!=nil{return written,err}
			if err:=CacheOnSubject(ctx,driver,subject,"traits",u);err!=nil{return written,err};written++
		}
	}
	return written,nil
}

var goalPatterns=[]*regexp.Regexp{
	regexp.MustCompile(`(?i)\bI (?:want|wanna|need|plan|hope) to ([^.?!]+)`),
	regexp.MustCompile(`(?i)\bI(?:'m| am) (?:trying|working|learning) (?:to|on) ([^.?!]+)`),
	regexp.MustCompile(`(?i)\bI(?:'m| am) (?:going to|gonna) ([^.?!]+)`),
	regexp.MustCompile(`(?i)\bmy goal (?:is|was) (?:to )?([^.?!]+)`),
	regexp.MustCompile(`(?i)\bI aim to ([^.?!]+)`),
	regexp.MustCompile(`(?i)\bI want ([^.?!]+)`),
	regexp.MustCompile(`(?i)\bI(?:'d| would) like to ([^.?!]+)`),
}
var badGoalPrefixes=[]string{"i_","i_m_","i_am_","im_","my_","we_","we_re_","we_are_","you_","they_","the_user_","user_"}

func scanGoalSentences(text string)[]string{
	out:=[]string{}
	for _,re:=range goalPatterns{for _,m:=range re.FindAllStringSubmatch(text,-1){if len(m)<2{continue};p:=strings.Trim(strings.Join(strings.Fields(m[len(m)-1])," ")," ,.;:");if len(p)>=8&&len(p)<=200{out=append(out,p)}}}
	return out
}
func cleanGoalName(name string)bool{n:=strings.ToLower(strings.TrimSpace(name));if len(n)<4||n=="none"||n=="null"||n=="n_a"||n=="na"{return false};for _,p:=range badGoalPrefixes{if n==strings.TrimSuffix(p,"_")||strings.HasPrefix(n,p){return false}};return true}

func resolveGoalSpeaker(ctx context.Context,driver Queryer,groupID string,episodeUUIDs []string)(string,error){
	raw,err:=driver.Query(ctx,`SELECT record::id(out) AS entity_uuid FROM mentions WHERE group_id = $g AND record::id(in) IN $eps;`,map[string]any{"g":groupID,"eps":episodeUUIDs});if err!=nil{return "",err}
	counts:=map[string]int{};for _,r:=range UnwrapRows(raw){if id:=stringFromAny(r["entity_uuid"]);id!=""{counts[id]++}}
	best:="";n:=0;for id,c:=range counts{if c>n{best=id;n=c}};if best!=""{return best,nil}
	for _,q:=range []string{`SELECT uuid FROM entity WHERE group_id = $g AND 'user' IN labels ORDER BY created_at DESC LIMIT 1;`,`SELECT uuid FROM entity WHERE group_id = $g ORDER BY created_at DESC LIMIT 1;`}{
		raw,err=driver.Query(ctx,q,map[string]any{"g":groupID});if err!=nil{return "",err};rows:=UnwrapRows(raw);if len(rows)>0{return stringFromAny(rows[0]["uuid"]),nil}
	}
	return "",nil
}

func SynthesizeGoals(ctx context.Context,driver Queryer,llm LLMClient,embedder Embedder,groupID string,episodeUUIDs []string)(int,error){
	if len(episodeUUIDs)==0{return 0,nil}
	raw,err:=driver.Query(ctx,"SELECT uuid, content FROM episode WHERE group_id = $g AND uuid IN $u;",map[string]any{"g":groupID,"u":episodeUUIDs});if err!=nil{return 0,err}
	sentences:=[]string{};for _,r:=range UnwrapRows(raw){sentences=append(sentences,scanGoalSentences(stringFromAny(r["content"]))...)};if len(sentences)==0{return 0,nil}
	existingRaw,err:=driver.Query(ctx,`SELECT name FROM entity WHERE group_id = $g AND 'goal' IN labels;`,map[string]any{"g":groupID});if err!=nil{return 0,err};existing:=[]string{};for _,r:=range UnwrapRows(existingRaw){existing=append(existing,stringFromAny(r["name"]))}
	accepted:=[]map[string]any{}
	if synth,ok:=llm.(Synthesizer);ok{
		var b strings.Builder;b.WriteString("GOAL CANDIDATES:\n");for i,s:=range sentences{fmt.Fprintf(&b,"[%d] %s\n",i,s)};if len(existing)>0{b.WriteString("\nEXISTING_GOALS: "+strings.Join(existing,", "))}
		if response,e:=synth.Synthesize(ctx,"Return JSON array of durable goals with name, description, domain, time_horizon, confidence.",b.String());e==nil{
			if parsed,ok:=ParseJSONLoose(response).([]any);ok{for _,item:=range parsed{m:=mapFromAny(item);name:=SnakeCase(stringFromAny(m["name"]));if !cleanGoalName(name){continue};conf:=.6;if v,ok:=toFloat(m["confidence"]);ok{conf=v};if conf<0{conf=0};if conf>1{conf=1};accepted=append(accepted,map[string]any{"name":name,"description":stringFromAny(m["description"]),"domain":stringFromAny(m["domain"]),"time_horizon":stringFromAny(m["time_horizon"]),"confidence":conf})}}
		}
	}
	if len(accepted)==0{name:=SnakeCase(sentences[0]);if !cleanGoalName(name){return 0,nil};accepted=[]map[string]any{{"name":name,"description":sentences[0],"domain":"","time_horizon":"unknown","confidence":.55}}}
	subject,err:=resolveGoalSpeaker(ctx,driver,groupID,episodeUUIDs);if err!=nil{return 0,err};if subject==""{return 0,nil}
	now:=utcNow();written:=0
	for _,goal:=range accepted{
		name:=stringFromAny(goal["name"]);description:=stringFromAny(goal["description"]);conf,_:=toFloat(goal["confidence"])
		u,err:=UpsertSyntheticEntity(ctx,driver,groupID,name,description,"goal",now);if err!=nil{return written,err};fact:=description;if fact==""{fact="pursues goal: "+name}
		extra:=map[string]any{"domain":func()any{v:=stringFromAny(goal["domain"]);if v==""{return nil};return v}(),"time_horizon":stringFromAny(goal["time_horizon"])}
		if _,err:=UpsertSyntheticEdge(ctx,driver,embedder,groupID,subject,u,"pursues_goal",fact,"goal",conf,nil,nil,"persistent",false,nil,extra,"",now);err!=nil{return written,err}
		if err:=CacheOnSubject(ctx,driver,subject,"goals_active",u);err!=nil{return written,err};written++
	}
	return written,nil
}
