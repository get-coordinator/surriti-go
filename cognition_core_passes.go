package surriti

import (
	"context"
	"math"
	"regexp"
	"strings"
	"time"
)

type affectLexEntry struct{ emotion string; polarity,weight float64 }
var affectLex=map[string]affectLexEntry{
	"frustrated":{"frustration",-0.7,1},"annoyed":{"frustration",-0.5,.8},"angry":{"frustration",-0.8,1},"stuck":{"frustration",-0.5,.7},"hate":{"frustration",-0.7,.8},
	"tired":{"fatigue",-0.4,.6},"exhausted":{"fatigue",-0.7,.9},"sad":{"sadness",-0.6,.8},"worried":{"anxiety",-0.5,.7},"anxious":{"anxiety",-0.6,.8},"nervous":{"anxiety",-0.4,.6},"embarrassed":{"embarrassment",-0.5,.7},
	"excited":{"excitement",.7,.9},"love":{"excitement",.7,.9},"happy":{"joy",.7,.9},"great":{"joy",.4,.5},"awesome":{"excitement",.7,.8},"amazing":{"excitement",.7,.8},"proud":{"pride",.7,.9},"confident":{"confidence",.6,.8},"winning":{"excitement",.6,.7},
	"urgent":{"urgency",0,.9},"asap":{"urgency",0,.9},"deadline":{"urgency",-0.2,.8},"confused":{"uncertainty",-0.3,.7},"unsure":{"uncertainty",-0.2,.6},"maybe":{"uncertainty",-0.1,.4},
}
var affectTokenRE=regexp.MustCompile("[a-zA-Z']+")

func ScoreAffect(text string) map[string]any {
	if text==""{return map[string]any{}}
	type pw struct{p,w float64};bags:=map[string][]pw{}
	for _,tok:=range affectTokenRE.FindAllString(strings.ToLower(text),-1){if e,ok:=affectLex[tok];ok{bags[e.emotion]=append(bags[e.emotion],pw{e.polarity,e.weight})}}
	if len(bags)==0{return map[string]any{}}
	dominant:="";best:=-1.0
	for emotion,vals:=range bags{sum:=0.0;for _,x:=range vals{sum+=x.w};if sum>best{best=sum;dominant=emotion}}
	vals:=bags[dominant];sumW,sumP:=0.0,0.0;for _,x:=range vals{sumW+=x.w;sumP+=x.p*x.w}
	intensity:=math.Min(1,sumW/2);polarity:=sumP/math.Max(1e-6,sumW)
	return map[string]any{"emotion":dominant,"polarity":math.Round(polarity*1000)/1000,"intensity":math.Round(intensity*1000)/1000}
}

func TagEpisodeAffect(ctx context.Context,driver Queryer,groupID string,episodeUUIDs []string)(int,error){
	if len(episodeUUIDs)==0{return 0,nil}
	raw,err:=driver.Query(ctx,"SELECT uuid, content FROM episode WHERE group_id = $g AND uuid IN $u;",map[string]any{"g":groupID,"u":episodeUUIDs});if err!=nil{return 0,err}
	tagged:=0
	for _,r:=range UnwrapRows(raw){a:=ScoreAffect(stringFromAny(r["content"]));if len(a)==0{continue}
		if _,err:=driver.Query(ctx,"UPDATE episode SET affect = $a WHERE uuid = $u;",map[string]any{"u":r["uuid"],"a":a});err!=nil{return tagged,err}
		if _,err:=driver.Query(ctx,`UPDATE relates_to SET valence = $v, intensity = math::max([intensity OR 0, $i]) WHERE group_id = $g AND $u IN episodes;`,map[string]any{"g":groupID,"u":r["uuid"],"v":a["polarity"],"i":a["intensity"]});err!=nil{return tagged,err};tagged++
	}
	return tagged,nil
}

var beliefRE=regexp.MustCompile(`(?i)\b(?:I think|I believe|I feel(?: like)?|feels like|seems(?: like)?|might be|I suspect|in my opinion|I guess|probably)\b`)
func LooksLikeBelief(text string)bool{return text!=""&&beliefRE.MatchString(text)}
func TagBeliefs(ctx context.Context,driver Queryer,groupID string,episodeUUIDs []string)(int,error){
	if len(episodeUUIDs)==0{return 0,nil}
	raw,err:=driver.Query(ctx,"SELECT uuid, content FROM episode WHERE group_id = $g AND uuid IN $u;",map[string]any{"g":groupID,"u":episodeUUIDs});if err!=nil{return 0,err}
	beliefEps:=[]string{};for _,r:=range UnwrapRows(raw){if LooksLikeBelief(stringFromAny(r["content"])){beliefEps=append(beliefEps,stringFromAny(r["uuid"]))}}
	if len(beliefEps)==0{return 0,nil}
	raw,err=driver.Query(ctx,`SELECT record::id(out) AS entity_uuid FROM mentions WHERE group_id = $g AND record::id(in) IN $eps;`,map[string]any{"g":groupID,"eps":beliefEps});if err!=nil{return 0,err}
	candidates:=[]string{};for _,r:=range UnwrapRows(raw){if id:=stringFromAny(r["entity_uuid"]);id!=""{candidates=append(candidates,id)}};candidates=orderedUniqueStrings(candidates);if len(candidates)==0{return 0,nil}
	raw,err=driver.Query(ctx,`SELECT uuid, attributes, record::id(in) AS speaker FROM relates_to WHERE group_id = $g AND record::id(in) IN $candidates AND episodes ANYINSIDE $eps AND is_belief = false AND status = 'active';`,map[string]any{"g":groupID,"candidates":candidates,"eps":beliefEps});if err!=nil{return 0,err}
	promoted:=0
	for _,r:=range UnwrapRows(raw){speaker:=stringFromAny(r["speaker"]);_,err:=driver.Query(ctx,`UPDATE relates_to SET is_belief = true, belief_holder = $h, attributes = object::extend(attributes, { memory_class: 'belief' }) WHERE uuid = $u;`,map[string]any{"u":r["uuid"],"h":speaker});if err!=nil{return promoted,err};promoted++}
	return promoted,nil
}

func RefreshAssociativeWeights(ctx context.Context,driver Queryer,groupID string,overrides map[string]float64)(int,error){
	raw,err:=driver.Query(ctx,`SELECT *, record::id(in) AS source_node_uuid, record::id(out) AS target_node_uuid FROM relates_to WHERE group_id = $g AND status = "active";`,map[string]any{"g":groupID});if err!=nil{return 0,err}
	edges:=[]EntityEdge{};for _,r:=range UnwrapRows(raw){edges=append(edges,ParseEdge(r))};if len(edges)==0{return 0,nil}
	entityFreq:=map[string]int{};episodeFreq:=map[string]int{}
	for _,e:=range edges{entityFreq[e.SourceNodeUUID]++;entityFreq[e.TargetNodeUUID]++;for _,ep:=range e.Episodes{episodeFreq[ep]++}}
	now:=utcNow();updated:=0
	for _,e:=range edges{decay:=EffectiveConfidence(e,now,overrides);touched:=(entityFreq[e.SourceNodeUUID]-1)+(entityFreq[e.TargetNodeUUID]-1);for _,ep:=range e.Episodes{if n:=episodeFreq[ep]-1;n>0{touched+=n}};boost:=math.Min(1,float64(touched)/12);weight:=math.Max(0,math.Min(4,decay*(1+boost)))
		if _,err:=driver.Query(ctx,"UPDATE relates_to SET weight = $w WHERE uuid = $u;",map[string]any{"u":e.UUID,"w":weight});err!=nil{return updated,err};updated++}
	return updated,nil
}

func SilenceInactiveEdges(ctx context.Context,driver Queryer,groupID string,minAgeDays,activationThreshold float64,limit int)(int,error){
	if limit<=0{limit=500};cutoff:=utcNow().Add(-time.Duration(math.Max(0,minAgeDays)*24*float64(time.Hour)))
	raw,err:=driver.Query(ctx,`SELECT * FROM relates_to WHERE group_id = $group_id AND status = "active" AND created_at < $cutoff ORDER BY created_at ASC LIMIT $limit;`,map[string]any{"group_id":groupID,"cutoff":cutoff,"limit":limit});if err!=nil{return 0,err}
	silenced:=0
	for _,r:=range UnwrapRows(raw){e:=ParseEdge(r);if IsDecayProtected(e)||Activation(e,time.Time{})>=activationThreshold{continue};if _,err:=driver.Query(ctx,`UPDATE relates_to SET status = "silent" WHERE uuid = $uuid;`,map[string]any{"uuid":e.UUID});err==nil{silenced++}}
	return silenced,nil
}
