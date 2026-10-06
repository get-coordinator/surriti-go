package surriti

import (
	"context"
	"sort"
	"time"
)

const (
	reinforcedThreshold=3
	persistentThreshold=7
	persistentMinSpanDays=7.0
	reinforcedEpisodesKey="activation_reinforced_episodes"
)

func ReinforceRecentEdges(ctx context.Context,driver Queryer,groupID string,episodeUUIDs []string)(int,error){
	if len(episodeUUIDs)==0{return 0,nil}
	raw,err:=driver.Query(ctx,`
SELECT uuid, episodes, valid_at, last_reinforced_at, stability,
       reinforcement_count, attributes
FROM relates_to
WHERE group_id = $g
  AND episodes ANYINSIDE $eps;`,map[string]any{"g":groupID,"eps":episodeUUIDs})
	if err!=nil{return 0,err}
	rows:=UnwrapRows(raw);if len(rows)==0{return 0,nil}
	all:=map[string]struct{}{}
	for _,row:=range rows{for _,u:=range asStringSlice(row["episodes"]){if u!=""{all[u]=struct{}{}}}}
	epTimes:=map[string]time.Time{}
	if len(all)>0{
		ids:=make([]string,0,len(all));for u:=range all{ids=append(ids,u)}
		raw,err:=driver.Query(ctx,"SELECT uuid, reference_time FROM episode WHERE uuid IN $u;",map[string]any{"u":ids});if err!=nil{return 0,err}
		for _,row:=range UnwrapRows(raw){if t:=coerceTime(row["reference_time"]);t!=nil{epTimes[stringFromAny(row["uuid"])]=*t}}
	}
	updated:=0
	for _,row:=range rows{
		unique:=[]string{};seen:=map[string]struct{}{}
		for _,u:=range asStringSlice(row["episodes"]){if u!=""{if _,ok:=seen[u];!ok{seen[u]=struct{}{};unique=append(unique,u)}}}
		count:=len(unique);if count<=0{continue}
		times:=[]time.Time{};for _,u:=range unique{if t,ok:=epTimes[u];ok{times=append(times,t)}}
		var lastSeen *time.Time;spanDays:=0.0
		if len(times)>0{sort.Slice(times,func(i,j int)bool{return times[i].Before(times[j])});t:=times[len(times)-1];lastSeen=&t;if len(times)>=2{spanDays=times[len(times)-1].Sub(times[0]).Hours()/24}}
		stability:=stringFromAny(row["stability"]);if stability==""{stability="episodic"}
		if stability!="consolidated"{
			if count>=persistentThreshold&&spanDays>=persistentMinSpanDays{stability="persistent"}else if count>=reinforcedThreshold&&stability=="episodic"{stability="reinforced"}
		}
		attrs:=cloneMap(mapFromAny(row["attributes"]));recorded:=map[string]struct{}{}
		for _,u:=range asStringSlice(attrs[reinforcedEpisodesKey]){recorded[u]=struct{}{}}
		for _,u:=range unique{
			if _,ok:=recorded[u];ok{continue}
			when:=utcNow();if t,ok:=epTimes[u];ok{when=t}else if lastSeen!=nil{when=*lastSeen}
			attrs=RecordActivationEvent(attrs,when,ActivationReinforcementWeight,ActivationMaxExactEvents)
			recorded[u]=struct{}{}
		}
		markers:=make([]string,0,len(recorded));for u:=range recorded{markers=append(markers,u)};sort.Strings(markers);attrs[reinforcedEpisodesKey]=markers
		_,err:=driver.Query(ctx,`
UPDATE relates_to SET
 reinforcement_count = $c,
 last_reinforced_at = $t,
 stability = $s,
 attributes = $attributes
WHERE uuid = $u;`,map[string]any{"u":row["uuid"],"c":count,"t":lastSeen,"s":stability,"attributes":attrs})
		if err!=nil{return updated,err};updated++
	}
	return updated,nil
}

func ReinforceEdgesOnRecall(ctx context.Context,driver Queryer,groupID string,edgeUUIDs []string,amount int)(int,error){
	uuids:=[]string{};seen:=map[string]struct{}{}
	for _,u:=range edgeUUIDs{if u!=""{if _,ok:=seen[u];!ok{seen[u]=struct{}{};uuids=append(uuids,u)}}}
	if len(uuids)==0{return 0,nil};if amount<1{amount=1}
	now:=utcNow()
	raw,err:=driver.Query(ctx,`
SELECT *, record::id(in) AS source_node_uuid, record::id(out) AS target_node_uuid
FROM relates_to
WHERE group_id = $group_id AND uuid IN $uuids;`,map[string]any{"group_id":groupID,"uuids":uuids})
	if err!=nil{return 0,err}
	updated:=0
	for _,row:=range UnwrapRows(raw){
		uid:=stringFromAny(row["uuid"]);if uid==""{continue}
		attrs:=cloneMap(mapFromAny(row["attributes"]))
		attrs=RecordActivationEvent(attrs,now,ActivationRecallWeight*float64(amount),ActivationMaxExactEvents)
		enriched:=cloneMap(row);enriched["attributes"]=attrs;enriched["recall_count"]=intFromAny(row["recall_count"])+amount;enriched["last_recalled_at"]=now
		score:=EffectiveConfidence(ParseEdge(enriched),now,nil)
		_,err:=driver.Query(ctx,`
UPDATE relates_to SET
 recall_count = $recall_count,
 last_recalled_at = $last_recalled_at,
 decay_score = $decay_score,
 attributes = $attributes
WHERE group_id = $group_id AND uuid = $uuid;`,map[string]any{"group_id":groupID,"uuid":uid,"recall_count":enriched["recall_count"],"last_recalled_at":now,"decay_score":score,"attributes":attrs})
		if err!=nil{return updated,err};updated++
	}
	return updated,nil
}
