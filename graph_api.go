package surriti

import (
	"context"
	"os"
	"strings"
	"time"
)

func (s *Surriti) GetEntityNode(ctx context.Context,uuid string)(*EntityNode,error){
	raw,err:=s.Driver.Query(ctx,"SELECT * FROM entity WHERE uuid = $u LIMIT 1;",map[string]any{"u":uuid})
	if err!=nil{return nil,err}
	rows:=UnwrapRows(raw);if len(rows)==0{return nil,nil};n:=ParseEntity(rows[0]);return &n,nil
}

func (s *Surriti) GetEntityEdge(ctx context.Context,uuid string)(*EntityEdge,error){
	raw,err:=s.Driver.Query(ctx,"SELECT * FROM relates_to WHERE uuid = $u LIMIT 1;",map[string]any{"u":uuid})
	if err!=nil{return nil,err}
	rows:=UnwrapRows(raw);if len(rows)==0{return nil,nil};e:=ParseEdge(rows[0]);return &e,nil
}

func (s *Surriti) GetEpisode(ctx context.Context,uuid string)(*EpisodicNode,error){
	raw,err:=s.Driver.Query(ctx,"SELECT * FROM episode WHERE uuid = $u LIMIT 1;",map[string]any{"u":uuid})
	if err!=nil{return nil,err}
	rows:=UnwrapRows(raw);if len(rows)==0{return nil,nil};e:=ParseEpisode(rows[0]);return &e,nil
}

func (s *Surriti) SaveNode(ctx context.Context,node EntityNode)(EntityNode,error){
	if len(node.NameEmbedding)==0{
		emb,err:=s.Embedder.Create(ctx,node.Name);if err!=nil{return EntityNode{},err};node.NameEmbedding=emb
	}
	_,err:=s.Driver.Query(ctx,`
UPSERT type::record("entity", $uuid) MERGE {
 uuid: $uuid, group_id: $group_id, name: $name,
 summary: $summary, labels: $labels, attributes: $attributes,
 name_embedding: $emb, created_at: $created_at
};`,map[string]any{"uuid":node.UUID,"group_id":node.GroupID,"name":node.Name,"summary":node.Summary,"labels":node.Labels,"attributes":node.Attributes,"emb":node.NameEmbedding,"created_at":node.CreatedAt})
	return node,err
}

func (s *Surriti) SaveEdge(ctx context.Context,edge EntityEdge)(EntityEdge,error){
	if len(edge.FactEmbedding)==0&&edge.Fact!=""{
		emb,err:=s.Embedder.Create(ctx,edge.Fact);if err!=nil{return EntityEdge{},err};edge.FactEmbedding=emb
	}
	_,err:=s.Driver.Query(ctx,`
UPDATE relates_to MERGE {
 name: $name, fact: $fact, fact_embedding: $emb,
 episodes: $episodes, valid_at: $valid_at,
 invalid_at: $invalid_at, expired_at: $expired_at,
 attributes: $attributes
} WHERE uuid = $uuid;`,map[string]any{"uuid":edge.UUID,"name":edge.Name,"fact":edge.Fact,"emb":edge.FactEmbedding,"episodes":edge.Episodes,"valid_at":edge.ValidAt,"invalid_at":edge.InvalidAt,"expired_at":edge.ExpiredAt,"attributes":edge.Attributes})
	return edge,err
}

func (s *Surriti) RemoveEdge(ctx context.Context,edgeUUID string)error{
	_,err:=s.Driver.Query(ctx,`
DELETE memory_ref WHERE out IN (SELECT VALUE id FROM relates_to WHERE uuid = $u);
DELETE relates_to WHERE uuid = $u;`,map[string]any{"u":edgeUUID})
	return err
}

func (s *Surriti) RemoveEpisode(ctx context.Context,episodeUUID string)error{
	raw,err:=s.Driver.Query(ctx,"SELECT * FROM relates_to WHERE $ep IN episodes;",map[string]any{"ep":episodeUUID})
	if err!=nil{return err}
	sole:=[]string{};shared:=[]string{}
	for _,row:=range UnwrapRows(raw){
		uid:=stringFromAny(row["uuid"]);eps:=asStringSlice(row["episodes"])
		if len(eps)==1&&eps[0]==episodeUUID{sole=append(sole,uid)}else{shared=append(shared,uid)}
	}
	if len(sole)>0{
		if _,err:=s.Driver.Query(ctx,"DELETE memory_ref WHERE record::id(out) IN $u;",map[string]any{"u":sole});err!=nil{return err}
		if _,err:=s.Driver.Query(ctx,"DELETE relates_to WHERE uuid IN $u;",map[string]any{"u":sole});err!=nil{return err}
	}
	if len(shared)>0{
		if _,err:=s.Driver.Query(ctx,`UPDATE relates_to SET episodes = array::filter(episodes, |$x| $x != $ep) WHERE uuid IN $u;`,map[string]any{"u":shared,"ep":episodeUUID});err!=nil{return err}
	}
	if _,err:=s.Driver.Query(ctx,"DELETE mentions WHERE in = type::record('episode', $ep);",map[string]any{"ep":episodeUUID});err!=nil{return err}
	_,err=s.Driver.Query(ctx,"DELETE episode WHERE uuid = $ep;",map[string]any{"ep":episodeUUID})
	return err
}

func destructiveAllowed()bool{
	v:=strings.ToLower(strings.TrimSpace(os.Getenv("SURRITI_ALLOW_DESTRUCTIVE")))
	return v=="1"||v=="true"||v=="yes"
}

func (s *Surriti) DeleteGroup(ctx context.Context,groupID string)error{
	if !destructiveAllowed(){return &destructiveDisabledError{operation:"Surriti.delete_group()"}}
	for _,table:=range []string{"memory_ref","mentions","relates_to","has_member","episode","entity","community"}{
		if _,err:=s.Driver.Query(ctx,"DELETE "+table+" WHERE group_id = $g;",map[string]any{"g":groupID});err!=nil{return err}
	}
	return nil
}

type destructiveDisabledError struct{operation string}
func(e *destructiveDisabledError)Error()string{return e.operation+" is disabled; set SURRITI_ALLOW_DESTRUCTIVE=1 to allow destructive operations"}

func (s *Surriti) RemoveNode(ctx context.Context,entityUUID string)error{
	_,err:=s.Driver.Query(ctx,`
DELETE memory_ref WHERE in = type::record('entity', $u)
 OR out IN (SELECT VALUE id FROM relates_to WHERE in = type::record('entity', $u) OR out = type::record('entity', $u));
DELETE relates_to WHERE in = type::record('entity', $u) OR out = type::record('entity', $u);
DELETE mentions WHERE out = type::record('entity', $u);
DELETE has_member WHERE out = type::record('entity', $u);
DELETE entity WHERE uuid = $u;`,map[string]any{"u":entityUUID})
	return err
}

func (s *Surriti) ForgetMemoryForParticipant(ctx context.Context,groupID,participantID,factUUID string,role *string,invalidAt *time.Time)(int,error){
	at:=utcNow();if invalidAt!=nil{at=invalidAt.UTC()}
	params:=map[string]any{"group_id":groupID,"viewer_id":participantID,"fact_uuid":factUUID,"invalid_at":at}
	roleClause:=""
	if role!=nil&&*role!=""{roleClause=" AND role = $role";params["role"]=*role}
	query:="UPDATE memory_ref SET invalid_at = $invalid_at "+
		"WHERE ($group_id = \"\" OR group_id = $group_id) "+
		"AND viewer_id = $viewer_id "+
		"AND fact_uuid = $fact_uuid "+
		"AND invalid_at IS NONE"+roleClause+" RETURN AFTER;"
	raw,err:=s.Driver.Query(ctx,query,params)
	if err!=nil{return 0,err}
	return len(UnwrapRows(raw)),nil
}

func (s *Surriti) FilterFactsForParticipant(ctx context.Context,groupID,participantID string,facts []EntityEdge)([]EntityEdge,map[string][]map[string]any,error){
	if len(facts)==0{return []EntityEdge{},map[string][]map[string]any{},nil}
	uuids:=[]string{};for _,f:=range facts{if f.UUID!=""{uuids=append(uuids,f.UUID)}}
	if len(uuids)==0{return []EntityEdge{},map[string][]map[string]any{},nil}
	raw,err:=s.Driver.Query(ctx,`
SELECT uuid, role, source_actor_uuid, episode_uuid, conversation_id,
       valid_at, invalid_at, fact_uuid
FROM memory_ref
WHERE group_id = $group_id
 AND viewer_id = $participant_id
 AND fact_uuid IN $fact_uuids
 AND invalid_at IS NONE;`,map[string]any{"group_id":groupID,"participant_id":participantID,"fact_uuids":uuids})
	if err!=nil{return nil,nil,err}
	refs:=map[string][]map[string]any{}
	for _,row:=range UnwrapRows(raw){id:=stringFromAny(row["fact_uuid"]);if id!=""{refs[id]=append(refs[id],row)}}
	allowed:=[]EntityEdge{};for _,fact:=range facts{if _,ok:=refs[fact.UUID];ok{allowed=append(allowed,fact)}}
	return allowed,refs,nil
}

func (s *Surriti) RecallForParticipant(ctx context.Context,query,participantID string,depth string,limit int,includeInvalid bool)(SearchResults,error){
	_ = depth
	raw,err:=s.Driver.Query(ctx,`
SELECT group_id, role, source_actor_uuid, episode_uuid, conversation_id,
       valid_at, fact_uuid
FROM memory_ref WHERE viewer_id = $viewer_id
 AND invalid_at IS NONE;`,map[string]any{"viewer_id":participantID})
	if err!=nil{return SearchResults{},err}
	refs:=UnwrapRows(raw);allowed:=[]string{};seen:=map[string]struct{}{}
	for _,row:=range refs{id:=stringFromAny(row["fact_uuid"]);if id!=""{if _,ok:=seen[id];!ok{seen[id]=struct{}{};allowed=append(allowed,id)}}}
	if len(allowed)==0{return SearchResults{Edges:[]EntityEdge{},Nodes:[]EntityNode{},Episodes:[]EpisodicNode{},Communities:[]CommunityNode{},Scores:map[string]float64{}},nil}
	cfg:=DefaultSearchConfig();if limit!=0{cfg.Limit=limit};cfg.OnlyValid=!includeInvalid
	var emb []float64
	if query!=""{emb,err=s.Embedder.Create(ctx,query);if err!=nil{return SearchResults{},err}}
	result,err:=HybridSearch(ctx,s.Driver,query,emb,nil,&cfg,nil,allowed);if err!=nil{return SearchResults{},err}
	byFact:=map[string][]map[string]any{}
	for _,ref:=range refs{id:=stringFromAny(ref["fact_uuid"]);byFact[id]=append(byFact[id],ref)}
	for i:=range result.Edges{
		meta:=byFact[result.Edges[i].UUID];result.Edges[i].ReferenceMetadata=meta
		roles:=[]string{};roleSeen:=map[string]struct{}{}
		for _,r:=range meta{role:=stringFromAny(r["role"]);if _,ok:=roleSeen[role];!ok{roleSeen[role]=struct{}{};roles=append(roles,role)}}
		result.Edges[i].ReferenceRoles=roles
	}
	return result,nil
}

func (s *Surriti) GrantMemoryAuthorized(ctx context.Context,sourceUserID,factUUID string)(bool,error){
	raw,err:=s.Driver.Query(ctx,`SELECT uuid FROM memory_ref WHERE viewer_id = $source AND role = 'asserted' AND invalid_at IS NONE AND fact_uuid = $fact_uuid LIMIT 1;`,map[string]any{"source":sourceUserID,"fact_uuid":factUUID})
	if err!=nil{return false,err};return len(UnwrapRows(raw))>0,nil
}

func (s *Surriti) GrantMemoryToParticipant(ctx context.Context,sourceUserID,recipientUserID,factUUID string)(bool,error){
	raw,err:=s.Driver.Query(ctx,`
SELECT group_id, source_actor_uuid, episode_uuid, conversation_id, valid_at
FROM memory_ref WHERE viewer_id = $source AND role = 'asserted'
 AND invalid_at IS NONE AND fact_uuid = $fact_uuid LIMIT 1;`,map[string]any{"source":sourceUserID,"fact_uuid":factUUID})
	if err!=nil{return false,err}
	rows:=UnwrapRows(raw);if len(rows)==0{return false,nil};row:=rows[0]
	groupID:=stringFromAny(row["group_id"])
	recipient,err:=s.UpsertUser(ctx,groupID,recipientUserID,"","");if err!=nil{return false,err}
	episodeUUID:=stringFromAny(row["episode_uuid"]);if episodeUUID==""{episodeUUID="grant:"+factUUID}
	var sourceActor *string;if v:=stringFromAny(row["source_actor_uuid"]);v!=""{sourceActor=&v}
	var conversation *string;if v:=stringFromAny(row["conversation_id"]);v!=""{conversation=&v}
	validAt:=utcNow()
	if err:=s.upsertMemoryRef(ctx,groupID,recipient.UUID,recipientUserID,factUUID,"granted",sourceActor,episodeUUID,conversation,validAt);err!=nil{return false,err}
	return true,nil
}

func (s *Surriti) RevokeMemoryFromParticipant(ctx context.Context,sourceUserID,recipientUserID,factUUID string)(int,error){
	ok,err:=s.GrantMemoryAuthorized(ctx,sourceUserID,factUUID);if err!=nil||!ok{return 0,err}
	role:="granted";return s.ForgetMemoryForParticipant(ctx,"",recipientUserID,factUUID,&role,nil)
}
