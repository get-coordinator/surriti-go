package surriti

import (
	"context"
	"time"
)

func (s *Surriti) Inspect(ctx context.Context, groupID *string, limit int) (map[string]any, error) {
	if limit == 0 { limit = 1000 }
	report := map[string]any{
		"generated_at": utcNow().Format(time.RFC3339Nano),
		"group_id": groupID,
		"database": "unknown",
	}
	errorsOut := []map[string]string{}
	where := ""
	params := map[string]any{"lim":limit}
	if groupID != nil && *groupID != "" { where="WHERE group_id = $g"; params["g"]=*groupID }
	whereAnd := func() string { if where=="" { return "WHERE " }; return where+" AND " }
	safe := func(label,q string) []map[string]any {
		raw,err:=s.Driver.Query(ctx,q,params)
		if err!=nil { errorsOut=append(errorsOut,map[string]string{"check":label,"message":err.Error()});return nil }
		return UnwrapRows(raw)
	}
	dup:=safe("duplicate_entity_candidates",
		"SELECT canonical_name, count() AS cnt, array::group(uuid) AS uuids FROM entity "+whereAnd()+
			"canonical_name IS NOT NONE GROUP BY canonical_name HAVING count() > 1 ORDER BY cnt DESC LIMIT $lim;")
	outDup:=[]map[string]any{}
	for _,r:=range dup{outDup=append(outDup,map[string]any{"canonical_name":r["canonical_name"],"count":r["cnt"],"uuids":r["uuids"]})}
	report["duplicate_entity_candidates"]=outDup
	conf:=safe("unresolved_conflict_groups",
		"SELECT conflict_group_id, count() AS cnt FROM relates_to "+whereAnd()+
			"conflict_group_id IS NOT NONE AND status = 'needs_resolution' GROUP BY conflict_group_id ORDER BY cnt DESC LIMIT $lim;")
	report["unresolved_conflict_groups"]=len(conf)
	mentionScope:="";if groupID!=nil&&*groupID!=""{mentionScope="WHERE group_id = $g"}
	orphanEp:=safe("orphan_episodes","SELECT uuid, name, source, created_at FROM episode "+whereAnd()+
		"uuid NOT IN (SELECT record::id(in) FROM mentions "+mentionScope+") LIMIT $lim;")
	oe:=[]map[string]any{};for _,r:=range orphanEp{oe=append(oe,map[string]any{"uuid":r["uuid"],"name":r["name"],"source":r["source"]})};report["orphan_episodes"]=oe
	relScope:="";if groupID!=nil&&*groupID!=""{relScope="WHERE group_id = $g"}
	orphanEnt:=safe("orphan_entities","SELECT uuid, name, canonical_name, created_at FROM entity "+whereAnd()+
		"uuid NOT IN (SELECT record::id(in) FROM relates_to "+relScope+") AND uuid NOT IN (SELECT record::id(out) FROM relates_to "+relScope+") LIMIT $lim;")
	oen:=[]map[string]any{};for _,r:=range orphanEnt{oen=append(oen,map[string]any{"uuid":r["uuid"],"name":r["name"],"canonical_name":r["canonical_name"]})};report["orphan_entities"]=oen
	report["facts_without_source_episodes"]=len(safe("facts_without_source_episodes","SELECT uuid FROM relates_to "+whereAnd()+"(episodes IS NONE OR array::len(episodes) = 0) LIMIT $lim;"))
	stale:=safe("stale_active_facts_sample","SELECT uuid, fact, name, valid_at FROM relates_to "+whereAnd()+"invalid_at IS NONE AND expired_at IS NONE AND status = 'active' AND valid_at IS NOT NONE ORDER BY valid_at ASC LIMIT $lim;")
	ss:=[]map[string]any{};for _,r:=range stale{ss=append(ss,map[string]any{"uuid":r["uuid"],"fact":r["fact"],"valid_at":r["valid_at"]})};report["stale_active_facts_sample"]=ss
	report["low_confidence_facts"]=len(safe("low_confidence_facts","SELECT uuid FROM relates_to "+whereAnd()+"confidence IS NOT NONE AND confidence < 0.5 ORDER BY confidence ASC LIMIT $lim;"))
	toCounts:=func(rows []map[string]any,key string)map[string]any{m:=map[string]any{};for _,r:=range rows{k:=stringFromAny(r[key]);if k==""{k="unknown"};m[k]=r["cnt"]};return m}
	report["counts_by_memory_class"]=toCounts(safe("counts_by_memory_class","SELECT attributes.memory_class AS mc, count() AS cnt FROM relates_to "+func()string{if where!=""{return where+" "};return ""}()+"GROUP BY mc ORDER BY cnt DESC;"),"mc")
	report["counts_by_status"]=toCounts(safe("counts_by_status","SELECT status, count() AS cnt FROM relates_to "+func()string{if where!=""{return where+" "};return ""}()+"GROUP BY status ORDER BY cnt DESC;"),"status")
	report["counts_by_source_type"]=toCounts(safe("counts_by_source_type","SELECT source_type, count() AS cnt FROM relates_to "+func()string{if where!=""{return where+" "};return ""}()+"GROUP BY source_type ORDER BY cnt DESC;"),"source_type")
	cog:=safe("cognition_backlog_unprocessed","SELECT count() AS cnt FROM episode "+whereAnd()+"cognition_processed_at IS NONE;")
	if len(cog)>0{report["cognition_backlog_unprocessed"]=cog[0]["cnt"]}else{report["cognition_backlog_unprocessed"]=0}
	last:=safe("last_cognition","SELECT cognition_version, cognition_processed_at FROM episode "+whereAnd()+"cognition_processed_at IS NOT NONE ORDER BY cognition_processed_at DESC LIMIT 1;")
	if len(last)>0{report["last_cognition"]=map[string]any{"version":last[0]["cognition_version"],"processed_at":last[0]["cognition_processed_at"]}}else{report["last_cognition"]=nil}
	missing:=safe("facts_missing_relation_frame","SELECT count() AS cnt FROM relates_to "+whereAnd()+"canonical_name IS NONE;")
	if len(missing)>0{report["facts_missing_relation_frame"]=missing[0]["cnt"]}else{report["facts_missing_relation_frame"]=0}
	for _,table:=range []string{"entity","episode","relates_to","community"}{
		rows:=safe("total_"+table,"SELECT count() AS cnt FROM "+table+" "+func()string{if where!=""{return where+";"};return ";"}())
		if len(rows)>0{report["total_"+table]=rows[0]["cnt"]}else{report["total_"+table]=0}
	}
	if len(errorsOut)>0{report["errors"]=errorsOut}
	return report,nil
}

func (s *Surriti) Explain(ctx context.Context, edgeUUID string, groupID *string) (map[string]any,error) {
	params:=map[string]any{"uuid":edgeUUID};extra:=""
	if groupID!=nil&&*groupID!=""{params["g"]=*groupID;extra=" AND group_id = $g"}
	raw,err:=s.Driver.Query(ctx,"SELECT * FROM relates_to WHERE uuid = $uuid"+extra+" LIMIT 1;",params);if err!=nil{return nil,err}
	rows:=UnwrapRows(raw);if len(rows)==0{return nil,nil};edge:=ParseEdge(rows[0])
	ex:=map[string]any{"edge":map[string]any{
		"uuid":edge.UUID,"name":edge.Name,"fact":edge.Fact,"group_id":edge.GroupID,"status":edge.Status,
		"confidence":edge.Confidence,"memory_class":edge.MemoryClass,"source_type":edge.SourceType,
		"valid_at":edge.ValidAt,"invalid_at":edge.InvalidAt,"expired_at":edge.ExpiredAt,
		"decay_score":edge.DecayScore,"weight":edge.Weight,"reinforcement_count":edge.ReinforcementCount,
		"stability":edge.Stability,"conflict_group_id":edge.ConflictGroupID,"supersedes":edge.Supersedes,
		"superseded_by":edge.SupersededBy,"canonical_name":edge.CanonicalName,"attributes":edge.Attributes,"created_at":edge.CreatedAt,
	}}
	entitySummary:=func(uuid string)(map[string]any,error){
		raw,err:=s.Driver.Query(ctx,"SELECT * FROM entity WHERE uuid = $u;",map[string]any{"u":uuid,"group_id":edge.GroupID});if err!=nil{return nil,err}
		rs:=UnwrapRows(raw);if len(rs)==0{return nil,nil};n:=ParseEntity(rs[0]);sum:=n.ProfileSummary;if sum==""{sum=n.Summary}
		return map[string]any{"uuid":n.UUID,"name":n.Name,"canonical_name":n.CanonicalName,"labels":n.Labels,"summary":sum},nil
	}
	if edge.SourceNodeUUID!=""{v,e:=entitySummary(edge.SourceNodeUUID);if e!=nil{return nil,e};if v!=nil{ex["source_entity"]=v}}
	if edge.TargetNodeUUID!=""{v,e:=entitySummary(edge.TargetNodeUUID);if e!=nil{return nil,e};if v!=nil{ex["target_entity"]=v}}
	if len(edge.Episodes)>0{
		raw,err:=s.Driver.Query(ctx,"SELECT uuid, name, content, source, reference_time, created_at FROM episode WHERE uuid IN $uuids ORDER BY reference_time DESC LIMIT 20;",map[string]any{"uuids":edge.Episodes});if err!=nil{return nil,err}
		eps:=[]map[string]any{};for _,r:=range UnwrapRows(raw){content:=stringFromAny(r["content"]);if len(content)>500{content=content[:500]};eps=append(eps,map[string]any{"uuid":r["uuid"],"name":r["name"],"content_preview":content,"source":r["source"],"reference_time":r["reference_time"]})};ex["source_episodes"]=eps
	}
	if edge.CanonicalName!=""{
		raw,err:=s.Driver.Query(ctx,"SELECT * FROM relation_frame WHERE canonical_name = $cn LIMIT 1;",map[string]any{"cn":edge.CanonicalName});if err!=nil{return nil,err}
		rs:=UnwrapRows(raw);if len(rs)>0{fr:=map[string]any{};for k,v:=range rs[0]{if !strings.HasSuffix(k,"_embedding"){fr[k]=v}};ex["relation_frame"]=fr}
	}
	if len(edge.Supersedes)>0{ex["supersedes_count"]=len(edge.Supersedes);if len(edge.Supersedes)<=5{raw,err:=s.Driver.Query(ctx,"SELECT uuid, fact, name, valid_at, invalid_at FROM relates_to WHERE uuid IN $uuids;",map[string]any{"uuids":edge.Supersedes});if err!=nil{return nil,err};ex["supersedes_details"]=UnwrapRows(raw)}}
	if edge.SupersededBy!=nil{raw,err:=s.Driver.Query(ctx,"SELECT uuid, fact, name, valid_at, invalid_at FROM relates_to WHERE uuid IN $uuids;",map[string]any{"uuids":[]string{*edge.SupersededBy}});if err!=nil{return nil,err};ex["superseded_by_details"]=UnwrapRows(raw)}
	return ex,nil
}

func (s *Surriti) GetNodesAndEdgesByEpisode(ctx context.Context, episodeUUIDs []string)(SearchResults,error){
	if len(episodeUUIDs)==0{return SearchResults{Edges:[]EntityEdge{},Nodes:[]EntityNode{},Episodes:[]EpisodicNode{},Communities:[]CommunityNode{},Scores:map[string]float64{}},nil}
	raw,err:=s.Driver.Query(ctx,"SELECT * FROM relates_to WHERE episodes ANYINSIDE $eps;",map[string]any{"eps":episodeUUIDs});if err!=nil{return SearchResults{},err}
	edges:=[]EntityEdge{};for _,r:=range UnwrapRows(raw){edges=append(edges,ParseEdge(r))}
	epIDs:=make([]string,len(episodeUUIDs));for i,u:=range episodeUUIDs{epIDs[i]="episode:"+u}
	raw,err=s.Driver.Query(ctx,`SELECT * FROM (SELECT out AS rec FROM mentions WHERE in IN $ep_ids).rec.*;`,map[string]any{"ep_ids":epIDs});if err!=nil{return SearchResults{},err}
	nodeRows:=UnwrapRows(raw)
	if len(nodeRows)==0{
		ids:=[]string{};for _,e:=range edges{ids=append(ids,e.SourceNodeUUID,e.TargetNodeUUID)};ids=orderedUniqueStrings(ids)
		if len(ids)>0{raw,err=s.Driver.Query(ctx,"SELECT * FROM entity WHERE uuid IN $ids;",map[string]any{"ids":ids});if err!=nil{return SearchResults{},err};nodeRows=UnwrapRows(raw)}
	}
	nodes:=[]EntityNode{};for _,r:=range nodeRows{nodes=append(nodes,ParseEntity(r))}
	raw,err=s.Driver.Query(ctx,"SELECT * FROM episode WHERE uuid IN $eps;",map[string]any{"eps":episodeUUIDs});if err!=nil{return SearchResults{},err}
	eps:=[]EpisodicNode{};for _,r:=range UnwrapRows(raw){eps=append(eps,ParseEpisode(r))}
	return SearchResults{Edges:edges,Nodes:nodes,Episodes:eps,Communities:[]CommunityNode{},Scores:map[string]float64{}},nil
}
