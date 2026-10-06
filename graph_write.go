package surriti

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

func entityNameKey(name string) string {
	return strings.Join(strings.Fields(strings.ToLower(name)), " ")
}

func MakeMemoryRefKey(groupID, actorUUID, factUUID, role, episodeUUID string) string {
	return strings.Join([]string{groupID, actorUUID, factUUID, role, episodeUUID}, "::")
}

func parseISOTime(value *string) *time.Time {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil
	}
	text := strings.TrimSpace(*value)
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, text); err == nil {
			u := t.UTC()
			return &u
		}
	}
	return nil
}

func (s *Surriti) saveEpisode(ctx context.Context, episode EpisodicNode) error {
	_, err := s.Driver.Query(ctx, `
CREATE type::record("episode", $uuid) CONTENT {
    uuid: $uuid,
    group_id: $group_id,
    name: $name,
    source: $source,
    source_description: $source_description,
    content: $content,
    reference_time: $reference_time,
    created_at: $created_at,
    entity_edges: [],
    ingestion_complete: false
};`, map[string]any{
		"uuid": episode.UUID,
		"group_id": episode.GroupID,
		"name": episode.Name,
		"source": string(episode.Source),
		"source_description": episode.SourceDescription,
		"content": episode.Content,
		"reference_time": episode.ReferenceTime,
		"created_at": episode.CreatedAt,
	})
	return err
}

func (s *Surriti) UpsertUser(ctx context.Context, groupID, userID, displayName, summary string) (EntityNode, error) {
	uid := userID
	if uid == "" {
		uid = groupID
	}
	if uid == "" {
		return EntityNode{}, fmt.Errorf("%w: upsert_user requires a non-empty user_id or group_id", ErrConfig)
	}
	result, err := s.Driver.Query(ctx,
		"SELECT * FROM entity WHERE group_id = $g AND name = $n LIMIT 1;",
		map[string]any{"g": groupID, "n": uid},
	)
	if err != nil { return EntityNode{}, err }
	rows := UnwrapRows(result)
	if len(rows) > 0 {
		existing := ParseEntity(rows[0])
		attrs := cloneMap(existing.Attributes)
		changed := false
		if displayName != "" && stringFromAny(attrs["display_name"]) != displayName {
			attrs["display_name"] = displayName
			changed = true
		}
		newSummary := existing.Summary
		if summary != "" { newSummary = summary }
		newLabels := sortedStringsUnique(append(append([]string(nil), existing.Labels...), "User"))
		if !stringSlicesEqual(newLabels, existing.Labels) {
			changed = true
		}
		if changed || newSummary != existing.Summary {
			_, err = s.Driver.Query(ctx, `
UPDATE type::record("entity", $uuid) SET
    summary = $summary,
    labels = $labels,
    attributes = $attributes;`, map[string]any{
				"uuid":existing.UUID,"summary":newSummary,"labels":newLabels,"attributes":attrs,
			})
			if err != nil { return EntityNode{}, err }
			existing.Attributes = attrs
			existing.Summary = newSummary
			existing.Labels = newLabels
		}
		return existing,nil
	}

	embedding, err := s.Embedder.Create(ctx, uid)
	if err != nil { return EntityNode{}, err }
	node := NewEntityNode(uid, groupID)
	node.Summary = summary
	node.Labels = []string{"User"}
	node.NameEmbedding = embedding
	if displayName != "" {
		node.Attributes = map[string]any{"display_name":displayName}
	}
	_, err = s.Driver.Query(ctx, `
CREATE type::record("entity", $uuid) CONTENT {
    uuid: $uuid,
    group_id: $group_id,
    name: $name,
    summary: $summary,
    labels: $labels,
    attributes: $attributes,
    name_embedding: $emb,
    created_at: $created_at
};`, map[string]any{
		"uuid":node.UUID,"group_id":node.GroupID,"name":node.Name,"summary":node.Summary,
		"labels":node.Labels,"attributes":node.Attributes,"emb":node.NameEmbedding,"created_at":node.CreatedAt,
	})
	if err == nil { return node,nil }
	if !strings.Contains(err.Error(),"entity_name_uniq") { return EntityNode{},err }
	fallback, qerr := s.Driver.Query(ctx,
		"SELECT * FROM entity WHERE group_id = $g AND name = $n LIMIT 1;",
		map[string]any{"g":groupID,"n":uid},
	)
	if qerr != nil { return EntityNode{},qerr }
	rows=UnwrapRows(fallback)
	if len(rows)==0 { return EntityNode{},err }
	return ParseEntity(rows[0]),nil
}

func stringSlicesEqual(a,b []string) bool {
	if len(a)!=len(b){return false}
	for i:=range a {if a[i]!=b[i]{return false}}
	return true
}

func normalizeExtractedEntity(e ExtractedEntity) ExtractedEntity {
	if len(e.Labels)==0 { e.Labels=[]string{"Entity"} }
	return e
}

func (s *Surriti) upsertEntities(ctx context.Context, extracted []ExtractedEntity, groupID string, episodeUUID *string, episodeContext string) ([]EntityNode,error) {
	if len(extracted)==0 { return []EntityNode{},nil }
	for i:=range extracted { extracted[i]=normalizeExtractedEntity(extracted[i]) }

	resolvedByMention:=map[string]ResolvedEntity{}
	if s.AliasResolutionEnabled {
		resolved,err:=ResolveEntityMentions(ctx,s.Driver,s.Embedder,s.LLM,extracted,groupID,episodeContext,s.AliasResolutionThreshold,s.AliasResolutionLLM,true,episodeUUID,nil)
		if err!=nil{return nil,err}
		for _,r:=range resolved { resolvedByMention[r.Mention.Name]=r }
	}

	seen:=map[string]ExtractedEntity{}
	order:=[]string{}
	for _,ext:=range extracted {
		key:=entityNameKey(ext.Name)
		if key=="" {continue}
		if _,ok:=seen[key];!ok {seen[key]=ext;order=append(order,key)}
	}
	deduped:=make([]ExtractedEntity,0,len(order))
	for _,key:=range order {deduped=append(deduped,seen[key])}

	raw,err:=s.Driver.Query(ctx,"SELECT * FROM entity WHERE group_id = $g;",map[string]any{"g":groupID})
	if err!=nil{return nil,err}
	existingByKey:=map[string]EntityNode{}
	duplicatesByKey:=map[string][]EntityNode{}
	for _,row:=range UnwrapRows(raw) {
		node:=ParseEntity(row)
		key:=entityNameKey(node.Name)
		if key=="" {continue}
		if _,ok:=existingByKey[key];!ok {existingByKey[key]=node} else {duplicatesByKey[key]=append(duplicatesByKey[key],node)}
	}
	for key,aliases:=range duplicatesByKey {
		canonical:=existingByKey[key]
		if err:=s.mergeEntityCaseDuplicates(ctx,canonical,aliases,groupID);err!=nil{return nil,err}
	}
	if len(resolvedByMention)>0 {
		uuidToNode:=map[string]EntityNode{}
		for _,n:=range existingByKey {uuidToNode[n.UUID]=n}
		for _,ext:=range extracted {
			hit,ok:=resolvedByMention[ext.Name]
			if !ok||hit.CanonicalUUID==nil {continue}
			key:=entityNameKey(ext.Name)
			if key=="" {continue}
			if _,ok:=existingByKey[key];ok{continue}
			if node,ok:=uuidToNode[*hit.CanonicalUUID];ok {
				existingByKey[key]=node
			} else if hit.Existing!=nil {
				existingByKey[key]=*hit.Existing
			}
		}
	}
	missing:=[]ExtractedEntity{}
	for _,ext:=range deduped {
		if _,ok:=existingByKey[entityNameKey(ext.Name)];!ok {missing=append(missing,ext)}
	}
	embeddings:=map[string][]float64{}
	if len(missing)>0 {
		names:=make([]string,len(missing))
		for i,e:=range missing {names[i]=e.Name}
		vecs,err:=CreateBatch(ctx,s.Embedder,names)
		if err!=nil{return nil,err}
		for i,e:=range missing {if i<len(vecs){embeddings[e.Name]=vecs[i]}}
	}

	results:=make([]EntityNode,0,len(deduped))
	for _,ext:=range deduped {
		key:=entityNameKey(ext.Name)
		if existing,ok:=existingByKey[key];ok {
			results=append(results,existing)
			continue
		}
		node:=NewEntityNode(ext.Name,groupID)
		node.Summary=ext.Summary
		node.Labels=append([]string(nil),ext.Labels...)
		node.NameEmbedding=embeddings[ext.Name]
		_,err:=s.Driver.Query(ctx,`
CREATE type::record("entity", $uuid) CONTENT {
    uuid: $uuid,
    group_id: $group_id,
    name: $name,
    summary: $summary,
    labels: $labels,
    attributes: {},
    name_embedding: $emb,
    canonical_name: $canonical_name,
    aliases: $aliases,
    created_at: $created_at
};`,map[string]any{
			"uuid":node.UUID,"group_id":node.GroupID,"name":node.Name,"summary":node.Summary,
			"labels":node.Labels,"emb":node.NameEmbedding,"canonical_name":node.Name,
			"aliases":[]string{node.Name},"created_at":node.CreatedAt,
		})
		if err==nil {
			results=append(results,node)
			existingByKey[key]=node
			continue
		}
		if !strings.Contains(err.Error(),"entity_name_uniq"){return nil,err}
		fallback,qerr:=s.Driver.Query(ctx,"SELECT * FROM entity WHERE group_id = $g AND name = $n LIMIT 1;",map[string]any{"g":groupID,"n":ext.Name})
		if qerr!=nil{return nil,qerr}
		rows:=UnwrapRows(fallback)
		if len(rows)==0 {
			all,qerr:=s.Driver.Query(ctx,"SELECT * FROM entity WHERE group_id = $g;",map[string]any{"g":groupID})
			if qerr!=nil{return nil,qerr}
			for _,row:=range UnwrapRows(all) {
				if entityNameKey(stringFromAny(row["name"]))==key {rows=[]map[string]any{row};break}
			}
		}
		if len(rows)==0{return nil,err}
		node=ParseEntity(rows[0])
		results=append(results,node)
		existingByKey[key]=node
	}
	return results,nil
}

func (s *Surriti) mergeEntityCaseDuplicates(ctx context.Context, canonical EntityNode, aliases []EntityNode, groupID string) error {
	ids:=[]string{}
	for _,a:=range aliases {if a.UUID!=canonical.UUID {ids=append(ids,a.UUID)}}
	if len(ids)==0{return nil}
	_,err:=s.Driver.Query(ctx,`
UPDATE relates_to
SET in = type::record("entity", $canonical)
WHERE group_id = $group_id AND record::id(in) IN $aliases;
UPDATE relates_to
SET out = type::record("entity", $canonical)
WHERE group_id = $group_id AND record::id(out) IN $aliases;
UPDATE mentions
SET out = type::record("entity", $canonical)
WHERE group_id = $group_id AND record::id(out) IN $aliases;
DELETE entity WHERE group_id = $group_id AND uuid IN $aliases;
`,map[string]any{"group_id":groupID,"canonical":canonical.UUID,"aliases":ids})
	return err
}

func (s *Surriti) findEquivalentEdge(ctx context.Context,groupID,subjectUUID,objectUUID,predicate,factText,qualifierHash string)(*EntityEdge,error){
	_ = factText
	key:=MakeFactKey(groupID,subjectUUID,predicate,objectUUID,qualifierHash)
	raw,err:=s.Driver.Query(ctx,`
SELECT * FROM relates_to
WHERE group_id = $group_id
    AND fact_key = $key
    AND invalid_at IS NONE
LIMIT 10;`,map[string]any{"group_id":groupID,"key":key})
	if err!=nil{return nil,err}
	rows:=UnwrapRows(raw)
	if len(rows)>0 {e:=ParseEdge(rows[0]);return &e,nil}
	raw,err=s.Driver.Query(ctx,`
SELECT * FROM relates_to
WHERE group_id = $group_id
    AND in = type::record("entity", $src)
    AND out = type::record("entity", $tgt)
    AND name = $name
    AND invalid_at IS NONE
LIMIT 10;`,map[string]any{"group_id":groupID,"src":subjectUUID,"tgt":objectUUID,"name":predicate})
	if err!=nil{return nil,err}
	rows=UnwrapRows(raw)
	if len(rows)==0{return nil,nil}
	e:=ParseEdge(rows[0]);return &e,nil
}

func factQualifierHash(factKey string) string {
	parts:=strings.Split(factKey,"::")
	if len(parts)>=5{return parts[4]}
	return ""
}

func normalizedMemoryClass(value string) string {
	value=strings.ToLower(strings.TrimSpace(value))
	if value=="" {return "objective"}
	return value
}

func (s *Surriti) closeSingletonSlot(ctx context.Context,groupID,subjectUUID,predicate,keepObjectUUID string,invalidAt time.Time,supersededBy,qualifierHash,memoryClass string)([]EntityEdge,error){
	raw,err:=s.Driver.Query(ctx,`
SELECT * FROM relates_to
WHERE group_id = $group_id
    AND in = type::record("entity", $src)
    AND name = $name
    AND status = "active"
    AND invalid_at IS NONE;`,map[string]any{"group_id":groupID,"src":subjectUUID,"name":predicate})
	if err!=nil{return nil,err}
	toClose:=[]EntityEdge{}
	newClass:=normalizedMemoryClass(memoryClass)
	for _,row:=range UnwrapRows(raw) {
		target:=stringFromAny(row["target_node_uuid"])
		if target==""{target=stripRecordID(row["out"])}
		if target==keepObjectUUID{continue}
		if factQualifierHash(stringFromAny(row["fact_key"]))!=qualifierHash{continue}
		attrs:=mapFromAny(row["attributes"])
		if normalizedMemoryClass(stringFromAny(attrs["memory_class"]))!=newClass{continue}
		toClose=append(toClose,ParseEdge(row))
	}
	if len(toClose)>0 {
		ids:=make([]string,len(toClose));for i,e:=range toClose{ids[i]=e.UUID}
		if err:=InvalidateEdges(ctx,s.Driver,ids,invalidAt,&supersededBy);err!=nil{return nil,err}
		for i:=range toClose {toClose[i].InvalidAt=&invalidAt;toClose[i].Status="superseded";toClose[i].SupersededBy=&supersededBy}
	}
	return toClose,nil
}

func (s *Surriti) applyReplaces(ctx context.Context,groupID,subjectUUID string,descriptors []string,excludeEdgeUUID *string,invalidAt time.Time,supersededBy string,similarityLimit int)([]EntityEdge,error){
	if len(descriptors)==0{return []EntityEdge{},nil}
	if similarityLimit==0{similarityLimit=5}
	seen:=map[string]EntityEdge{}
	for _,descriptor:=range descriptors {
		text:=strings.TrimSpace(descriptor);if text==""{continue}
		var emb []float64
		if v,err:=s.Embedder.Create(ctx,text);err==nil{emb=v}
		candidates,err:=FindSimilarEdges(ctx,s.Driver,text,emb,groupID,similarityLimit,nil,nil,true)
		if err!=nil{continue}
		for _,edge:=range candidates {
			if excludeEdgeUUID!=nil&&edge.UUID==*excludeEdgeUUID{continue}
			if edge.SourceNodeUUID!=subjectUUID||edge.InvalidAt!=nil||edge.Status!="active"{continue}
			if _,ok:=seen[edge.UUID];!ok{seen[edge.UUID]=edge}
		}
	}
	out:=make([]EntityEdge,0,len(seen));ids:=make([]string,0,len(seen))
	for _,edge:=range seen{out=append(out,edge);ids=append(ids,edge.UUID)}
	if len(ids)>0 {
		if err:=InvalidateEdges(ctx,s.Driver,ids,invalidAt,&supersededBy);err!=nil{return nil,err}
		for i:=range out {out[i].InvalidAt=&invalidAt;out[i].Status="superseded";out[i].SupersededBy=&supersededBy}
	}
	return out,nil
}

func (s *Surriti) terminateMatchingEdge(ctx context.Context,groupID,subjectUUID,objectUUID,predicate string,invalidAt time.Time)([]EntityEdge,error){
	raw,err:=s.Driver.Query(ctx,`
SELECT * FROM relates_to
WHERE group_id = $group_id
    AND in = type::record("entity", $src)
    AND out = type::record("entity", $tgt)
    AND name = $name
    AND status = "active"
    AND invalid_at IS NONE;`,map[string]any{"group_id":groupID,"src":subjectUUID,"tgt":objectUUID,"name":predicate})
	if err!=nil{return nil,err}
	rows:=UnwrapRows(raw);edges:=make([]EntityEdge,0,len(rows));ids:=make([]string,0,len(rows))
	for _,row:=range rows{e:=ParseEdge(row);edges=append(edges,e);ids=append(ids,e.UUID)}
	if len(ids)>0 {
		if err:=InvalidateEdges(ctx,s.Driver,ids,invalidAt,nil);err!=nil{return nil,err}
		for i:=range edges{edges[i].InvalidAt=&invalidAt;edges[i].Status="superseded"}
	}
	return edges,nil
}

func (s *Surriti) findActiveSlotPeers(ctx context.Context,groupID,subjectUUID,predicate,excludeObjectUUID,qualifierHash string)([]EntityEdge,error){
	raw,err:=s.Driver.Query(ctx,`
SELECT * FROM relates_to
WHERE group_id = $group_id
    AND in = type::record("entity", $src)
    AND name = $name
    AND status = "active"
    AND invalid_at IS NONE;`,map[string]any{"group_id":groupID,"src":subjectUUID,"name":predicate})
	if err!=nil{return nil,err}
	out:=[]EntityEdge{}
	for _,row:=range UnwrapRows(raw){
		target:=stringFromAny(row["target_node_uuid"]);if target==""{target=stripRecordID(row["out"])}
		if target==excludeObjectUUID||factQualifierHash(stringFromAny(row["fact_key"]))!=qualifierHash{continue}
		out=append(out,ParseEdge(row))
	}
	return out,nil
}

func (s *Surriti) markConflictGroup(ctx context.Context,edgeUUIDs []string,conflictGroupID string)error{
	if len(edgeUUIDs)==0{return nil}
	_,err:=s.Driver.Query(ctx,"UPDATE relates_to SET conflict_group_id = $cg WHERE uuid IN $uuids;",map[string]any{"uuids":edgeUUIDs,"cg":conflictGroupID})
	return err
}

func uniqueEdgeUUIDs(groups ...[]EntityEdge) []string {
	seen:=map[string]struct{}{};out:=[]string{}
	for _,group:=range groups{for _,e:=range group{if _,ok:=seen[e.UUID];!ok{seen[e.UUID]=struct{}{};out=append(out,e.UUID)}}}
	return out
}

func (s *Surriti) addFactEdge(ctx context.Context,fact ExtractedFact,subject,obj EntityNode,episode *EpisodicNode,groupID,sourceType string)(EntityEdge,[]EntityEdge,error){
	validAt:=utcNow()
	if v:=parseISOTime(fact.ValidAt);v!=nil {validAt=*v} else if episode!=nil {validAt=episode.ReferenceTime}
	invalidAt:=parseISOTime(fact.InvalidAt)
	req:=FrameClassificationRequest{Predicate:fact.Predicate,SampleSubject:subject.Name,SampleObject:obj.Name}
	if fact.SourceSpan!=nil {req.SourceSpan=*fact.SourceSpan} else {req.SourceSpan=fact.Fact}
	frame,hasFrame:=s.RelationFrames.Resolve(ctx,req,groupID)
	canonicalName:=fact.Predicate
	edgeName:=canonicalName
	subjUUID,objUUID:=subject.UUID,obj.UUID
	subjNode,objNode:=subject,obj
	if hasFrame {
		canonicalName=frame.CanonicalName;edgeName=canonicalName
		if frame.Directionality==DirectionSymmetric {
			ns,no:=NormalizeSymmetric(subjUUID,objUUID)
			if ns!=subjUUID||no!=objUUID {subjUUID,objUUID=ns,no;subjNode,objNode=obj,subject}
		}
	}
	op:=fact.Operation;if op==""{op=FactAssert}
	isSingleton:=false
	if op!=FactQualify && !(hasFrame&&frame.ContradictionPolicy==ContradictionUncertain) {
		if hasFrame&&frame.Cardinality==CardinalityOneCurrent {isSingleton=true}
		if !hasFrame&&fact.Singleton&&sourceType=="user"{isSingleton=true}
	}
	qhash:=QualifierHash(fact.Qualifiers)
	factText:=fact.Fact
	if factText==""{factText=fmt.Sprintf("%s %s %s.",subjNode.Name,edgeName,objNode.Name)}
	embedding,err:=s.Embedder.Create(ctx,factText);if err!=nil{return EntityEdge{},nil,err}
	existing,err:=s.findEquivalentEdge(ctx,groupID,subjUUID,objUUID,edgeName,factText,qhash);if err!=nil{return EntityEdge{},nil,err}
	if existing!=nil {
		if episode!=nil&&!containsString(existing.Episodes,episode.UUID){
			existing.Episodes=append(existing.Episodes,episode.UUID)
			_,err=s.Driver.Query(ctx,`UPDATE relates_to SET episodes = array::distinct(array::concat(episodes, $episodes)) WHERE uuid = $uuid;`,map[string]any{"uuid":existing.UUID,"episodes":[]string{episode.UUID}})
			if err!=nil{return EntityEdge{},nil,err}
		}
		return *existing,[]EntityEdge{},nil
	}
	edgeUUID:=newUUID()
	replacesClosed,err:=s.applyReplaces(ctx,groupID,subjUUID,fact.Replaces,nil,validAt,edgeUUID,5)
	if err!=nil{return EntityEdge{},nil,err}
	singletonClosed:=[]EntityEdge{}
	if isSingleton {
		singletonClosed,err=s.closeSingletonSlot(ctx,groupID,subjUUID,edgeName,objUUID,validAt,edgeUUID,qhash,normalizedMemoryClass(fact.MemoryClass))
		if err!=nil{return EntityEdge{},nil,err}
	}
	edgeStatus:="active";var conflictGroupID *string
	invalidated:=[]EntityEdge{}
	if len(singletonClosed)>0 {
		invalidated=append(invalidated,singletonClosed...)
	} else if hasFrame&&frame.ContradictionPolicy==ContradictionCoexist {
		invalidated=[]EntityEdge{}
	} else {
		invalidated,err=ResolveContradictions(ctx,s.Driver,s.LLM,factText,embedding,validAt,groupID,10,&fact,&edgeUUID,&subjUUID,&objUUID)
		if err!=nil{return EntityEdge{},nil,err}
	}
	seenInvalid:=map[string]struct{}{};for _,e:=range invalidated{seenInvalid[e.UUID]=struct{}{}}
	for _,e:=range replacesClosed{if _,ok:=seenInvalid[e.UUID];!ok{invalidated=append(invalidated,e);seenInvalid[e.UUID]=struct{}{}}}
	if len(invalidated)==0&&hasFrame&&frame.ContradictionPolicy==ContradictionUncertain{
		peers,err:=s.findActiveSlotPeers(ctx,groupID,subjUUID,edgeName,objUUID,qhash);if err!=nil{return EntityEdge{},nil,err}
		if len(peers)>0{
			cg:=newUUID();conflictGroupID=&cg;edgeStatus="needs_resolution"
			ids:=make([]string,len(peers));for i,p:=range peers{ids[i]=p.UUID}
			if err:=s.markConflictGroup(ctx,ids,cg);err!=nil{return EntityEdge{},nil,err}
		}
	}
	edge:=NewEntityEdge(subjUUID,objUUID,edgeName,groupID)
	edge.UUID=edgeUUID;edge.Fact=factText;edge.FactEmbedding=embedding
	if episode!=nil{edge.Episodes=[]string{episode.UUID}}
	edge.ValidAt=&validAt;edge.InvalidAt=invalidAt;edge.Status=edgeStatus;edge.SourceType=sourceType
	edge.Confidence=fact.Confidence;if edge.Confidence==0{edge.Confidence=1}
	edge.Temporal=fact.Temporal||(hasFrame&&frame.TemporalKind==TemporalState)
	edge.Singleton=isSingleton;edge.Domain=fact.Domain;edge.Supersedes=uniqueEdgeUUIDs(singletonClosed,replacesClosed)
	edge.FactKey=MakeFactKey(groupID,subjUUID,edgeName,objUUID,qhash)
	if hasFrame {id:=frame.UUID;edge.RelationFrameID=&id}
	edge.CanonicalName=canonicalName;edge.Qualifiers=mapFromAny(fact.Qualifiers);edge.Roles=map[string]string{}
	for k,v:=range fact.ArgumentRoles{edge.Roles[k]=v}
	edge.ConflictGroupID=conflictGroupID;edge.MemoryClass=normalizedMemoryClass(fact.MemoryClass)
	edge.Attributes=map[string]any{"memory_class":edge.MemoryClass}
	_,err=s.Driver.Query(ctx,`
RELATE (type::record("entity", $src))->relates_to->(type::record("entity", $tgt))
CONTENT {
    uuid: $uuid,
    group_id: $group_id,
    name: $name,
    fact: $fact,
    fact_embedding: $emb,
    episodes: $episodes,
    valid_at: $valid_at,
    invalid_at: $invalid_at,
    status: $status,
    polarity: $polarity,
    source_type: $source_type,
    confidence: $confidence,
    temporal: $temporal,
    singleton: $singleton,
    domain: $domain,
    supersedes: $supersedes,
    fact_key: $fact_key,
    relation_frame_id: $relation_frame_id,
    canonical_name: $canonical_name,
    qualifiers: $qualifiers,
    roles: $roles,
    conflict_group_id: $conflict_group_id,
    derived: $derived,
    derived_from: $derived_from,
    attributes: $attributes,
    created_at: $created_at
};`,map[string]any{
		"src":subjUUID,"tgt":objUUID,"uuid":edge.UUID,"group_id":edge.GroupID,"name":edge.Name,
		"fact":edge.Fact,"emb":edge.FactEmbedding,"episodes":edge.Episodes,"valid_at":edge.ValidAt,"invalid_at":edge.InvalidAt,
		"status":edge.Status,"polarity":edge.Polarity,"source_type":edge.SourceType,"confidence":edge.Confidence,
		"temporal":edge.Temporal,"singleton":edge.Singleton,"domain":edge.Domain,"supersedes":edge.Supersedes,
		"fact_key":edge.FactKey,"relation_frame_id":edge.RelationFrameID,"canonical_name":edge.CanonicalName,
		"qualifiers":edge.Qualifiers,"roles":edge.Roles,"conflict_group_id":edge.ConflictGroupID,"derived":edge.Derived,
		"derived_from":edge.DerivedFrom,"attributes":edge.Attributes,"created_at":edge.CreatedAt,
	})
	if err==nil{return edge,invalidated,nil}
	if !strings.Contains(err.Error(),"relates_to_fact_key_idx"){return EntityEdge{},nil,err}
	winner,qerr:=s.findEquivalentEdge(ctx,groupID,subjUUID,objUUID,edgeName,factText,qhash)
	if qerr!=nil{return EntityEdge{},nil,qerr}
	if winner==nil{return EntityEdge{},nil,err}
	if episode!=nil&&!containsString(winner.Episodes,episode.UUID){
		winner.Episodes=append(winner.Episodes,episode.UUID)
		_,qerr=s.Driver.Query(ctx,`UPDATE relates_to SET episodes = array::distinct(array::concat(episodes, $episodes)) WHERE uuid = $uuid;`,map[string]any{"uuid":winner.UUID,"episodes":[]string{episode.UUID}})
		if qerr!=nil{return EntityEdge{},nil,qerr}
	}
	return *winner,invalidated,nil
}

func (s *Surriti) linkMentions(ctx context.Context,episode EpisodicNode,entities []EntityNode,groupID string)([]EpisodicEdge,error){
	out:=[]EpisodicEdge{}
	for _,ent:=range entities{
		sum:=sha256.Sum256([]byte(groupID+"\x1f"+episode.UUID+"\x1f"+ent.UUID))
		mentionUUID:=hex.EncodeToString(sum[:])
		raw,err:=s.Driver.Query(ctx,"SELECT uuid FROM mentions WHERE uuid = $uuid LIMIT 1;",map[string]any{"uuid":mentionUUID})
		if err!=nil{return nil,err}
		if len(UnwrapRows(raw))>0{continue}
		edge:=EpisodicEdge{EdgeBase:EdgeBase{BaseModel:NewBaseModel(groupID),SourceNodeUUID:episode.UUID,TargetNodeUUID:ent.UUID}}
		edge.UUID=mentionUUID
		_,err=s.Driver.Query(ctx,`
RELATE (type::record("episode", $ep))->mentions->(type::record("entity", $en))
CONTENT { uuid: $uuid, group_id: $group_id, created_at: $created_at };`,map[string]any{"ep":episode.UUID,"en":ent.UUID,"uuid":edge.UUID,"group_id":edge.GroupID,"created_at":edge.CreatedAt})
		if err!=nil{return nil,err}
		out=append(out,edge)
	}
	return out,nil
}

func (s *Surriti) upsertMemoryRef(ctx context.Context,groupID,actorUUID,viewerID,factUUID,role string,sourceActorUUID *string,episodeUUID string,conversationID *string,validAt time.Time)error{
	switch role{case "asserted","witnessed","granted","endorsed","disputed":default:return fmt.Errorf("%w: unsupported memory_ref role %q",ErrConfig,role)}
	refUUID:=MakeMemoryRefKey(groupID,actorUUID,factUUID,role,episodeUUID)
	payload:=map[string]any{"uuid":refUUID,"group_id":groupID,"viewer_id":viewerID,"fact_uuid":factUUID,"role":role,"source_actor_uuid":sourceActorUUID,"episode_uuid":episodeUUID,"conversation_id":conversationID,"valid_at":validAt,"invalid_at":nil,"created_at":utcNow()}
	raw,err:=s.Driver.Query(ctx,"SELECT uuid FROM memory_ref WHERE uuid = $uuid LIMIT 1;",map[string]any{"uuid":refUUID})
	if err!=nil{return err}
	update:=func()error{_,err:=s.Driver.Query(ctx,`
UPDATE memory_ref SET
 group_id=$group_id, viewer_id=$viewer_id, fact_uuid=$fact_uuid, role=$role,
 source_actor_uuid=$source_actor_uuid, episode_uuid=$episode_uuid,
 conversation_id=$conversation_id, valid_at=$valid_at, invalid_at=$invalid_at
WHERE uuid=$uuid;`,payload);return err}
	if len(UnwrapRows(raw))>0{return update()}
	insert:=cloneMap(payload);insert["actor_uuid"]=actorUUID
	_,err=s.Driver.Query(ctx,`
INSERT RELATION INTO memory_ref {
 in: type::record("entity", $actor_uuid),
 out: (SELECT VALUE id FROM relates_to WHERE uuid = $fact_uuid LIMIT 1)[0],
 uuid: $uuid, group_id: $group_id, viewer_id: $viewer_id, fact_uuid: $fact_uuid,
 role: $role, source_actor_uuid: $source_actor_uuid, episode_uuid: $episode_uuid,
 conversation_id: $conversation_id, valid_at: $valid_at, invalid_at: $invalid_at,
 created_at: $created_at
};`,insert)
	if err==nil{return nil}
	if !strings.Contains(err.Error(),"memory_ref_uuid_idx"){return err}
	return update()
}

func (s *Surriti) writeMemoryRefsForFact(ctx context.Context,groupID,factUUID,speakerID string,participantIDs []string,episodeUUID string,conversationID *string,validAt time.Time)error{
	speaker,err:=s.UpsertUser(ctx,groupID,speakerID,"","");if err!=nil{return err}
	seen:=map[string]struct{}{}
	ensure:=func(userID,role string)error{
		if userID==""{return nil};if _,ok:=seen[userID];ok{return nil};seen[userID]=struct{}{}
		actor:=speaker
		if userID!=speakerID {actor,err=s.UpsertUser(ctx,groupID,userID,"","");if err!=nil{return err}}
		source:=speaker.UUID
		return s.upsertMemoryRef(ctx,groupID,actor.UUID,userID,factUUID,role,&source,episodeUUID,conversationID,validAt)
	}
	if err:=ensure(speakerID,"asserted");err!=nil{return err}
	for _,id:=range participantIDs{role:="witnessed";if id==speakerID{role="asserted"};if err:=ensure(id,role);err!=nil{return err}}
	return nil
}
