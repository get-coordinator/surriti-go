package surriti

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func UTCNowISO() string {
	return utcNow().Format(time.RFC3339Nano)
}

func ReadModelJSONable(value any) any {
	switch v := value.(type) {
	case nil, string, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, bool:
		return v
	case time.Time:
		return v.Format(time.RFC3339Nano)
	case *time.Time:
		if v == nil { return nil }
		return v.Format(time.RFC3339Nano)
	case []string:
		out := make([]any, len(v))
		for i, x := range v { out[i] = x }
		return out
	case []any:
		out := make([]any, len(v))
		for i, x := range v { out[i] = ReadModelJSONable(x) }
		return out
	case map[string]any:
		out := map[string]any{}
		for k, x := range v {
			lk := strings.ToLower(k)
			if strings.Contains(lk, "embedding") || strings.Contains(lk, "vector") {
				continue
			}
			out[k] = ReadModelJSONable(x)
		}
		return out
	default:
		if m, ok := toStringAnyMap(value); ok {
			return ReadModelJSONable(m)
		}
		return fmt.Sprint(value)
	}
}

func ReadModelRecordID(value any) string {
	raw := stringFromAny(value)
	if i := strings.IndexByte(raw, ':'); i >= 0 {
		raw = raw[i+1:]
	}
	return strings.Trim(raw, "\x60⟨⟩\'\\\"")
}

// unwrapReadModelRows matches read_models.py: legacy statement wrappers are
// flattened rather than taking only the final statement.
func unwrapReadModelRows(rows any) []map[string]any {
	if rows == nil { return []map[string]any{} }
	if row, ok := toStringAnyMap(rows); ok {
		if result, exists := row["result"]; exists {
			if m, ok := toStringAnyMap(result); ok { return []map[string]any{m} }
			if xs := mapsFromAnySlice(result); xs != nil { return xs }
		}
		return []map[string]any{row}
	}
	switch xs := rows.(type) {
	case []map[string]any:
		allWrapped := len(xs) > 0
		for _, row := range xs {
			if _, ok := row["result"]; !ok { allWrapped = false; break }
		}
		if allWrapped {
			out := []map[string]any{}
			for _, row := range xs {
				if m, ok := toStringAnyMap(row["result"]); ok {
					out = append(out, m)
				} else if nested := mapsFromAnySlice(row["result"]); nested != nil {
					out = append(out, nested...)
				}
			}
			return out
		}
		return xs
	case []any:
		if len(xs) == 0 { return []map[string]any{} }
		allWrapped := true
		for _, item := range xs {
			row, ok := toStringAnyMap(item)
			if !ok { continue }
			if _, exists := row["result"]; !exists { allWrapped = false; break }
		}
		if allWrapped {
			out := []map[string]any{}
			for _, item := range xs {
				row, ok := toStringAnyMap(item); if !ok { continue }
				if m, ok := toStringAnyMap(row["result"]); ok {
					out = append(out, m)
				} else if nested := mapsFromAnySlice(row["result"]); nested != nil {
					out = append(out, nested...)
				}
			}
			return out
		}
		out := []map[string]any{}
		for _, item := range xs {
			if m, ok := toStringAnyMap(item); ok { out = append(out, m) }
		}
		return out
	default:
		return []map[string]any{}
	}
}

func readModelQuery(ctx context.Context, driver Queryer, query string, vars map[string]any) ([]map[string]any, error) {
	if vars == nil { vars = map[string]any{} }
	rows, err := driver.Query(ctx, query, vars)
	if err != nil { return nil, err }
	return unwrapReadModelRows(rows), nil
}

func GraphNode(table string, row map[string]any) map[string]any {
	uuid := stringFromAny(row["uuid"])
	if uuid == "" { uuid = ReadModelRecordID(row["id"]) }
	labels := asStringSlice(row["labels"])
	if labels == nil { labels = []string{} }
	switch table {
	case "entity":
		if !containsString(labels, "Entity") { labels = append([]string{"Entity"}, labels...) }
	case "episode":
		labels = []string{"Episode"}
	case "community":
		labels = []string{"Community"}
	case "resource":
		labels = []string{"Library resource"}
	}
	name := stringFromAny(row["name"])
	if table == "resource" {
		name = stringFromAny(row["title"])
		if name == "" { name = stringFromAny(row["library_item_id"]) }
		if name == "" { name = stringFromAny(row["name"]) }
	}
	label := strings.Title(table)
	if len(labels) > 0 { label = labels[0] }
	summary := stringFromAny(row["summary"])
	if summary == "" { summary = stringFromAny(row["profile_summary"]) }
	attrs := mapFromAny(row["attributes"])
	edges := asStringSlice(row["entity_edges"])
	if edges == nil { edges = []string{} }
	return map[string]any{
		"id": uuid, "uuid": uuid, "table": table, "kind": table, "name": name,
		"label": label, "labels": labels, "group_id": stringFromAny(row["group_id"]),
		"summary": summary, "source": stringFromAny(row["source"]),
		"source_description": stringFromAny(row["source_description"]),
		"reference_time": ReadModelJSONable(row["reference_time"]),
		"created_at": ReadModelJSONable(row["created_at"]),
		"attributes": ReadModelJSONable(attrs), "entity_edges": edges,
		"raw": ReadModelJSONable(row), "degree": 0,
	}
}

func GraphEdge(table string, row map[string]any, source, target, name string) map[string]any {
	canonical := stringFromAny(row["canonical_name"])
	attrs := mapFromAny(row["attributes"])
	id := stringFromAny(row["uuid"])
	if id == "" { id = table+":"+source+":"+target+":"+name }
	label := canonical
	if label == "" { label = name }
	if label == "" { label = table }
	memoryClass := stringFromAny(attrs["memory_class"])
	if memoryClass == "" { memoryClass = "objective" }
	return map[string]any{
		"id": id, "uuid": id, "table": table, "kind": table,
		"source": source, "target": target, "name": name, "canonical_name": canonical,
		"label": label, "fact": stringFromAny(row["fact"]), "group_id": stringFromAny(row["group_id"]),
		"episodes": asStringSlice(row["episodes"]), "valid_at": ReadModelJSONable(row["valid_at"]),
		"invalid_at": ReadModelJSONable(row["invalid_at"]), "expired_at": ReadModelJSONable(row["expired_at"]),
		"created_at": ReadModelJSONable(row["created_at"]), "status": stringFromAny(row["status"]),
		"polarity": stringFromAny(row["polarity"]), "source_type": stringFromAny(row["source_type"]),
		"confidence": row["confidence"], "temporal": boolFromAny(row["temporal"]),
		"singleton": boolFromAny(row["singleton"]), "domain": stringFromAny(row["domain"]),
		"fact_key": stringFromAny(row["fact_key"]), "relation_frame_id": stringFromAny(row["relation_frame_id"]),
		"qualifiers": ReadModelJSONable(mapFromAny(row["qualifiers"])), "roles": ReadModelJSONable(mapFromAny(row["roles"])),
		"supersedes": asStringSlice(row["supersedes"]), "superseded_by": stringFromAny(row["superseded_by"]),
		"conflict_group_id": stringFromAny(row["conflict_group_id"]), "derived": boolFromAny(row["derived"]),
		"derived_from": stringFromAny(row["derived_from"]), "attributes": ReadModelJSONable(attrs),
		"is_belief": boolFromAny(row["is_belief"]), "belief_holder": stringFromAny(row["belief_holder"]),
		"weight": row["weight"], "decay_score": row["decay_score"], "reinforcement_count": row["reinforcement_count"],
		"stability": defaultString(stringFromAny(row["stability"]), "episodic"),
		"valence": row["valence"], "intensity": row["intensity"], "consolidates": asStringSlice(row["consolidates"]),
		"memory_class": memoryClass, "raw": ReadModelJSONable(row),
	}
}

func defaultString(v, fallback string) string { if v == "" { return fallback }; return v }

func resourceOwner(entities []map[string]any, groupID string) map[string]any {
	if !strings.HasPrefix(groupID, "user:") { return nil }
	userID := strings.SplitN(groupID, ":", 2)[1]
	for _, row := range entities {
		name := stringFromAny(row["name"])
		if name == userID || name == groupID { return row }
	}
	for _, row := range entities {
		if containsString(asStringSlice(row["labels"]), "User") { return row }
	}
	return nil
}

type ReadGraphParams struct {
	GroupID          string
	Limit            int
	View             string
	IncludeInvalid   bool
	AggregateMentions *bool
	AsOf             *time.Time
	Status           []string
	SourceType       []string
	CanonicalName    []string
	EdgeVisibility   string
	ConflictOnly     bool
	DerivedOnly      bool
	MinConfidence    *float64
	ValidAfter       *time.Time
	ValidBefore      *time.Time
	EgoUUID          *string
	EgoHops          int
}

func relatesFilter(p ReadGraphParams) (string, map[string]any) {
	clauses := []string{}
	out := map[string]any{}
	if p.GroupID != "" { clauses = append(clauses, "group_id = $group_id"); out["group_id"] = p.GroupID }
	if p.AsOf != nil {
		clauses = append(clauses,
			"(valid_at IS NONE OR valid_at <= $as_of)",
			"(invalid_at IS NONE OR invalid_at > $as_of)",
			"(expired_at IS NONE OR expired_at > $as_of)",
		)
		out["as_of"] = *p.AsOf
	} else if !p.IncludeInvalid {
		clauses = append(clauses, "(invalid_at IS NONE AND expired_at IS NONE)")
	}
	if len(p.Status) > 0 { clauses=append(clauses,"status IN $status"); out["status"]=p.Status }
	if len(p.SourceType) > 0 { clauses=append(clauses,"source_type IN $source_type"); out["source_type"]=p.SourceType }
	if len(p.CanonicalName) > 0 {
		clauses=append(clauses,"(canonical_name IN $canonical OR name IN $canonical)")
		canon:=make([]string,len(p.CanonicalName)); for i,v:=range p.CanonicalName{canon[i]=strings.ToLower(v)}
		out["canonical"]=canon
	}
	visibility:=p.EdgeVisibility; if visibility==""{visibility="all"}
	if p.ConflictOnly || visibility=="conflicts" { clauses=append(clauses,"conflict_group_id IS NOT NONE AND conflict_group_id != ''") }
	if p.DerivedOnly || visibility=="derived" { clauses=append(clauses,"derived = true") }
	if visibility=="non_derived" { clauses=append(clauses,"(derived IS NONE OR derived = false)") }
	if visibility=="invalidated" { clauses=append(clauses,"(invalid_at IS NOT NONE OR expired_at IS NOT NONE OR status = 'superseded')") }
	if visibility=="active" {
		clauses=append(clauses,"(invalid_at IS NONE AND expired_at IS NONE)","(status IS NONE OR status = '' OR status = 'active')")
	}
	if p.MinConfidence!=nil { clauses=append(clauses,"(confidence IS NONE OR confidence >= $min_conf)"); out["min_conf"]=*p.MinConfidence }
	if p.ValidAfter!=nil { clauses=append(clauses,"(valid_at IS NONE OR valid_at >= $valid_after)"); out["valid_after"]=*p.ValidAfter }
	if p.ValidBefore!=nil { clauses=append(clauses,"(valid_at IS NONE OR valid_at <= $valid_before)"); out["valid_before"]=*p.ValidBefore }
	if len(clauses)==0{return "",out}
	return "WHERE "+strings.Join(clauses," AND "),out
}

func ReadGraph(ctx context.Context, driver Queryer, p ReadGraphParams) (map[string]any,error) {
	limit:=p.Limit; if limit==0{limit=1500}; if limit<50{limit=50}; if limit>10000{limit=10000}
	view:=p.View; if view==""{view="truth"}
	apiView:=map[string]string{"truth":"entities","raw":"full","provenance":"full","conflicts":"entities","timeline":"entities","frames":"entities","self":"self","vitality":"entities"}[view]
	if apiView==""{apiView=view}
	aggregate:=true; if p.AggregateMentions!=nil{aggregate=*p.AggregateMentions}
	visibility:=p.EdgeVisibility; if visibility==""{visibility="all"}
	status:=append([]string(nil),p.Status...)
	includeInvalid:=p.IncludeInvalid
	if view=="truth"&&visibility=="all"{visibility="active";if len(status)==0{status=[]string{"active"}};includeInvalid=false}
	if view=="conflicts"{visibility="conflicts";if len(status)==0{status=[]string{"needs_resolution"}}}
	if view=="raw"{includeInvalid=true}
	p.EdgeVisibility=visibility;p.Status=status;p.IncludeInvalid=includeInvalid

	wantEntities:=apiView=="full"||apiView=="entities"||apiView=="self"
	wantEpisodes:=apiView=="full"||apiView=="episodes"
	wantSelfEpisodes:=apiView=="self"
	wantRelates:=apiView=="full"||apiView=="entities"||apiView=="vitality"
	wantMentions:=apiView=="full"||apiView=="episodes"||apiView=="self"
	whereGroup:="WHERE group_id = $group_id"
	base:=map[string]any{"group_id":p.GroupID,"limit":limit}
	relWhere,relParams:=relatesFilter(p);relParams["limit"]=limit

	var entities,episodes,resources,resourceEpisodes,communities,relates,mentions,members []map[string]any
	var err error
	if wantEntities { entities,err=readModelQuery(ctx,driver,"SELECT * FROM entity "+whereGroup+" LIMIT $limit;",base);if err!=nil{return nil,err} }
	epWhere:=whereGroup;if wantSelfEpisodes{epWhere="WHERE group_id = $group_id AND source CONTAINS 'self_'"}
	if wantEpisodes||wantSelfEpisodes { episodes,err=readModelQuery(ctx,driver,"SELECT * FROM episode "+epWhere+" LIMIT $limit;",base);if err!=nil{return nil,err} }
	resourceEpisodes,err=readModelQuery(ctx,driver,"SELECT * FROM episode WHERE group_id = $group_id AND source_description CONTAINS 'source:' LIMIT $limit;",base);if err!=nil{return nil,err}
	resources,err=readModelQuery(ctx,driver,"SELECT * FROM resource "+whereGroup+" LIMIT $limit;",base);if err!=nil{return nil,err}
	if wantEntities { communities,err=readModelQuery(ctx,driver,"SELECT * FROM community "+whereGroup+" LIMIT $limit;",base);if err!=nil{return nil,err} }
	if wantRelates { relates,err=readModelQuery(ctx,driver,"SELECT *, record::id(in) AS source_uuid, record::id(out) AS target_uuid FROM relates_to "+relWhere+" LIMIT $limit;",relParams);if err!=nil{return nil,err} }
	if wantMentions { mentions,err=readModelQuery(ctx,driver,"SELECT *, record::id(in) AS source_uuid, record::id(out) AS target_uuid FROM mentions "+whereGroup+" LIMIT $limit;",base);if err!=nil{return nil,err} }
	if wantEntities { members,err=readModelQuery(ctx,driver,"SELECT *, record::id(in) AS source_uuid, record::id(out) AS target_uuid FROM has_member "+whereGroup+" LIMIT $limit;",base);if err!=nil{return nil,err} }

	nodes:=map[string]map[string]any{}
	for _,group:=range []struct{table string;rows []map[string]any}{{"entity",entities},{"episode",episodes},{"episode",resourceEpisodes},{"resource",resources},{"community",communities}} {
		for _,row:=range group.rows{node:=GraphNode(group.table,row);nodes[stringFromAny(node["id"])]=node}
	}
	links:=[]map[string]any{}
	owner:=resourceOwner(entities,p.GroupID);ownerID:=""
	if owner!=nil{ownerID=stringFromAny(owner["uuid"]);if ownerID==""{ownerID=ReadModelRecordID(owner["id"])}}
	supportSeen:=map[string]struct{}{}
	for _,resource:=range resources{
		resourceID:=stringFromAny(resource["uuid"]);if resourceID==""{resourceID=ReadModelRecordID(resource["id"])}
		libraryID:=stringFromAny(resource["library_item_id"]);if resourceID==""||libraryID==""{continue}
		title:=stringFromAny(resource["title"]);if title==""{title=libraryID}
		relationship:=defaultString(stringFromAny(resource["relationship"]),"reference_material")
		if ownerID!=""&&nodes[ownerID]!=nil{
			links=append(links,GraphEdge("resource_reference",map[string]any{
				"uuid":"resource-owner:"+resourceID+":"+ownerID,"group_id":defaultString(stringFromAny(resource["group_id"]),p.GroupID),
				"source_type":"library","derived":true,"fact":title+" is "+strings.ReplaceAll(relationship,"_"," ")+" for this user.",
				"attributes":map[string]any{"library_item_id":libraryID,"resource_relation":"owner","relationship":relationship},
			},resourceID,ownerID,relationship))
		}
		sourceTag:="source:"+libraryID
		for _,ep:=range resourceEpisodes{
			if stringFromAny(ep["source_description"])!=sourceTag{continue}
			episodeID:=stringFromAny(ep["uuid"]);if episodeID==""{episodeID=ReadModelRecordID(ep["id"])}
			if nodes[resourceID]==nil||nodes[episodeID]==nil{continue}
			links=append(links,GraphEdge("resource_reference",map[string]any{
				"uuid":"resource-reference:"+resourceID+":"+episodeID,"group_id":defaultString(stringFromAny(resource["group_id"]),p.GroupID),
				"source_type":"library","fact":"This Library resource was consulted and produced this source memory.",
				"attributes":map[string]any{"library_item_id":libraryID,"resource_relation":"consulted_in"},
			},resourceID,episodeID,"consulted in"))
			for _,fact:=range relates{
				if !containsString(asStringSlice(fact["episodes"]),episodeID){continue}
				predicate:=stringFromAny(fact["canonical_name"]);if predicate==""{predicate=defaultString(stringFromAny(fact["name"]),"fact")}
				factUUID:=stringFromAny(fact["uuid"])
				src:=stringFromAny(fact["source_node_uuid"]);if src==""{src=stringFromAny(fact["source_uuid"])};if src==""{src=ReadModelRecordID(fact["in"])}
				tgt:=stringFromAny(fact["target_node_uuid"]);if tgt==""{tgt=stringFromAny(fact["target_uuid"])};if tgt==""{tgt=ReadModelRecordID(fact["out"])}
				for _,endpoint:=range []string{src,tgt}{
					if endpoint==""||endpoint==ownerID||nodes[endpoint]==nil{continue}
					key:=resourceID+""+factUUID+""+endpoint;if _,ok:=supportSeen[key];ok{continue};supportSeen[key]=struct{}{}
					links=append(links,GraphEdge("resource_reference",map[string]any{
						"uuid":"resource-support:"+resourceID+":"+factUUID+":"+endpoint,"group_id":defaultString(stringFromAny(resource["group_id"]),p.GroupID),
						"source_type":"library","derived":true,"fact":stringFromAny(fact["fact"]),
						"attributes":map[string]any{"library_item_id":libraryID,"resource_relation":"support","fact_uuid":factUUID,"predicate":predicate},
					},resourceID,endpoint,"supports "+predicate))
				}
			}
		}
	}
	for _,bundle:=range []struct{table string;rows []map[string]any;name string}{{"relates_to",relates,"relates_to"},{"has_member",members,"has_member"}} {
		for _,row:=range bundle.rows{
			src:=stringFromAny(row["source_node_uuid"]);if src==""{src=stringFromAny(row["source_uuid"])};if src==""{src=ReadModelRecordID(row["in"])}
			tgt:=stringFromAny(row["target_node_uuid"]);if tgt==""{tgt=stringFromAny(row["target_uuid"])};if tgt==""{tgt=ReadModelRecordID(row["out"])}
			if nodes[src]!=nil&&nodes[tgt]!=nil{name:=stringFromAny(row["name"]);if name==""{name=bundle.name};links=append(links,GraphEdge(bundle.table,row,src,tgt,name))}
		}
	}
	if aggregate{
		buckets:=map[string][]map[string]any{};keys:=map[string][2]string{}
		for _,row:=range mentions{
			src:=stringFromAny(row["source_uuid"]);if src==""{src=ReadModelRecordID(row["in"])}
			tgt:=stringFromAny(row["target_uuid"]);if tgt==""{tgt=ReadModelRecordID(row["out"])}
			if nodes[src]!=nil&&nodes[tgt]!=nil{key:=src+""+tgt;buckets[key]=append(buckets[key],row);keys[key]=[2]string{src,tgt}}
		}
		for key,rows:=range buckets{st:=keys[key];link:=GraphEdge("mentions",rows[0],st[0],st[1],"mentions");link["count"]=len(rows);link["aggregated"]=true;ids:=[]string{};for _,r:=range rows{if id:=stringFromAny(r["uuid"]);id!=""{ids=append(ids,id)}};link["uuids"]=ids;links=append(links,link)}
	}else{
		for _,row:=range mentions{
			src:=stringFromAny(row["source_uuid"]);if src==""{src=ReadModelRecordID(row["in"])}
			tgt:=stringFromAny(row["target_uuid"]);if tgt==""{tgt=ReadModelRecordID(row["out"])}
			if nodes[src]!=nil&&nodes[tgt]!=nil{links=append(links,GraphEdge("mentions",row,src,tgt,"mentions"))}
		}
	}
	if p.EgoUUID!=nil&&nodes[*p.EgoUUID]!=nil{
		hops:=p.EgoHops;if hops<1{hops=1};if hops>2{hops=2}
		adj:=map[string]map[string]struct{}{}
		add:=func(a,b string){if adj[a]==nil{adj[a]=map[string]struct{}{}};adj[a][b]=struct{}{}}
		for _,link:=range links{src:=stringFromAny(link["source"]);tgt:=stringFromAny(link["target"]);add(src,tgt);add(tgt,src)}
		keep:=map[string]struct{}{*p.EgoUUID:{}};frontier:=map[string]struct{}{*p.EgoUUID:{}}
		for i:=0;i<hops;i++{next:=map[string]struct{}{};for f:=range frontier{for n:=range adj[f]{if _,ok:=keep[n];!ok{keep[n]=struct{}{};next[n]=struct{}{}}}};frontier=next}
		for id:=range nodes{if _,ok:=keep[id];!ok{delete(nodes,id)}}
		filtered:=links[:0];for _,link:=range links{if nodes[stringFromAny(link["source"])]!=nil&&nodes[stringFromAny(link["target"])]!=nil{filtered=append(filtered,link)}};links=filtered
	}
	for _,link:=range links{for _,id:=range []string{stringFromAny(link["source"]),stringFromAny(link["target"])}{if n:=nodes[id];n!=nil{n["degree"]=intFromAny(n["degree"])+1}}}
	nodeList:=make([]map[string]any,0,len(nodes));for _,n:=range nodes{nodeList=append(nodeList,n)}
	return map[string]any{"nodes":nodeList,"links":links,"meta":map[string]any{
		"view":view,"api_view":apiView,"group_id":p.GroupID,"include_invalid":includeInvalid,"aggregate_mentions":aggregate,
		"node_count":len(nodes),"link_count":len(links),"generated_at":UTCNowISO(),
	}},nil
}

func ReadEntityProfile(ctx context.Context,driver Queryer,uuid,groupID string,factLimit int)(map[string]any,error){
	if factLimit==0{factLimit=20};if factLimit<1{factLimit=1};if factLimit>200{factLimit=200}
	rows,err:=readModelQuery(ctx,driver,"SELECT * FROM entity WHERE uuid = $uuid AND group_id = $g LIMIT 1;",map[string]any{"uuid":uuid,"g":groupID});if err!=nil{return nil,err};if len(rows)==0{return nil,nil}
	row:=rows[0]
	aliases,err:=readModelQuery(ctx,driver,"SELECT alias, normalized_alias, confidence, source_episode_uuid FROM entity_alias WHERE entity_uuid = $uuid AND group_id = $g;",map[string]any{"uuid":uuid,"g":groupID});if err!=nil{return nil,err}
	facts,err:=readModelQuery(ctx,driver,"SELECT *, record::id(in) AS s_uuid, record::id(out) AS t_uuid, time::unix(valid_at OR created_at) AS st FROM relates_to WHERE (record::id(in) = $uuid OR record::id(out) = $uuid) AND group_id = $g AND (invalid_at IS NONE OR invalid_at = NONE) ORDER BY st DESC LIMIT $limit;",map[string]any{"uuid":uuid,"g":groupID,"limit":factLimit});if err!=nil{return nil,err}
	canonical:=stringFromAny(row["canonical_name"]);if canonical==""{canonical=stringFromAny(row["name"])}
	return map[string]any{
		"uuid":defaultString(stringFromAny(row["uuid"]),uuid),"group_id":row["group_id"],"name":row["name"],"canonical_name":canonical,
		"aliases":asStringSlice(row["aliases"]),"alias_records":ReadModelJSONable(aliases),"labels":asStringSlice(row["labels"]),
		"summary":stringFromAny(row["summary"]),"profile_summary":stringFromAny(row["profile_summary"]),
		"salience":row["salience"],"mention_count":row["mention_count"],"last_seen_at":ReadModelJSONable(row["last_seen_at"]),
		"merged_into":row["merged_into"],"attributes":ReadModelJSONable(mapFromAny(row["attributes"])),"facts":ReadModelJSONable(facts),
		"generated_at":UTCNowISO(),
	},nil
}

func ReadEpisode(ctx context.Context,driver Queryer,uuid,groupID string)(map[string]any,error){
	rows,err:=readModelQuery(ctx,driver,"SELECT * FROM episode WHERE uuid = $uuid AND group_id = $g LIMIT 1;",map[string]any{"uuid":uuid,"g":groupID});if err!=nil{return nil,err};if len(rows)==0{return nil,nil}
	node:=GraphNode("episode",rows[0]);node["content"]=stringFromAny(rows[0]["content"])
	counts,err:=readModelQuery(ctx,driver,"SELECT count() AS c FROM mentions WHERE record::id(in) = $uuid GROUP ALL;",map[string]any{"uuid":uuid});if err!=nil{return nil,err}
	count:=0;if len(counts)>0{count=intFromAny(counts[0]["c"])};node["mention_count"]=count
	return node,nil
}

func ReadCognition(ctx context.Context,driver Queryer,aspect,groupID string,limit int)(map[string]any,error){
	if limit==0{limit=30};if limit<1{limit=1};if limit>1000{limit=1000}
	base:=map[string]any{"g":groupID,"limit":limit}
	if aspect=="summary"{
		cnt:=func(q string)(int,error){rows,err:=readModelQuery(ctx,driver,q,map[string]any{"g":groupID});if err!=nil{return 0,err};if len(rows)==0{return 0,nil};v:=rows[0]["count"];if v==nil{v=rows[0]["c"]};return intFromAny(v),nil}
		queries:=[]struct{k,q string}{
			{"beliefs","SELECT count() AS count FROM relates_to WHERE group_id = $g AND is_belief = true GROUP ALL;"},
			{"trait_nodes","SELECT count() AS count FROM entity WHERE group_id = $g AND labels CONTAINS 'trait' GROUP ALL;"},
			{"goal_nodes","SELECT count() AS count FROM entity WHERE group_id = $g AND labels CONTAINS 'goal' GROUP ALL;"},
			{"affect_tagged_episodes","SELECT count() AS count FROM episode WHERE group_id = $g AND (affect IS NOT NONE AND affect != {}) GROUP ALL;"},
			{"consolidated_edges","SELECT count() AS count FROM relates_to WHERE group_id = $g AND attributes.memory_class = 'consolidated' GROUP ALL;"},
			{"reinforced_edges","SELECT count() AS count FROM relates_to WHERE group_id = $g AND reinforcement_count IS NOT NONE AND reinforcement_count > 0 GROUP ALL;"},
			{"prediction_sidecars","SELECT count() AS count FROM community WHERE group_id = $g AND kind = 'prediction' GROUP ALL;"},
			{"procedural_episodes","SELECT count() AS count FROM episode WHERE group_id = $g AND interaction_pattern IS NOT NONE GROUP ALL;"},
		}
		out:=map[string]any{"group_id":groupID,"generated_at":UTCNowISO()}
		for _,item:=range queries{n,err:=cnt(item.q);if err!=nil{return nil,err};out[item.k]=n}
		return out,nil
	}
	if aspect=="predictions"{
		rows,err:=readModelQuery(ctx,driver,"SELECT payload, created_at FROM community WHERE kind = 'prediction' AND group_id = $g ORDER BY created_at DESC LIMIT 1;",map[string]any{"g":groupID});if err!=nil{return nil,err}
		var payload any;var refreshed any
		if len(rows)>0{payload=ReadModelJSONable(rows[0]["payload"]);if m,ok:=payload.(map[string]any);ok{refreshed=m["refreshed_at"]}else{refreshed=ReadModelJSONable(rows[0]["created_at"])}}
		return map[string]any{"group_id":groupID,"payload":payload,"refreshed_at":refreshed,"generated_at":UTCNowISO()},nil
	}
	type spec struct{table,key,predicate,fields string}
	specs:=map[string]spec{
		"beliefs":{"relates_to","beliefs","is_belief = true","uuid, group_id, fact, belief_holder, created_at, name"},
		"traits":{"entity","traits","labels CONTAINS 'trait'","uuid, name, group_id, summary, labels, created_at"},
		"goals":{"entity","goals","labels CONTAINS 'goal'","uuid, name, group_id, summary, labels, created_at"},
		"affect":{"episode","episodes","(affect IS NOT NONE AND affect != {})","uuid, name, group_id, affect, created_at"},
		"consolidated":{"relates_to","edges","attributes.memory_class = 'consolidated'","*"},
		"reinforced":{"relates_to","edges","reinforcement_count IS NOT NONE AND reinforcement_count > 0","*"},
	}
	s,ok:=specs[aspect];if !ok{return nil,fmt.Errorf("unsupported cognition aspect: %s",aspect)}
	rows,err:=readModelQuery(ctx,driver,"SELECT "+s.fields+" FROM "+s.table+" WHERE group_id = $g AND "+s.predicate+" ORDER BY created_at DESC LIMIT $limit;",base);if err!=nil{return nil,err}
	return map[string]any{"group_id":groupID,s.key:ReadModelJSONable(rows),"count":len(rows),"generated_at":UTCNowISO()},nil
}
