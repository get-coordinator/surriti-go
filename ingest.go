package surriti

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

type EntityTypePair struct {
	Source string
	Target string
}

type AddEpisodeRequest struct {
	Name                         string
	EpisodeBody                  string
	Source                       EpisodeType
	SourceDescription            string
	ReferenceTime                *time.Time
	GroupID                      string
	UUID                         *string
	UpdateCommunities            bool
	ExcludedEntityTypes          []string
	EntityTypes                  map[string]any
	PreviousEpisodeUUIDs         []string
	EdgeTypes                    map[string]any
	EdgeTypeMap                  map[EntityTypePair][]string
	CustomExtractionInstructions string
	SpeakerID                    *string
	SpeakerName                  *string
	ParticipantIDs               []string
	ConversationID               *string
	SourceType                   string
}

func (s *Surriti) RetrieveEpisodes(ctx context.Context, referenceTime *time.Time, lastN int, groupIDs []string, source *EpisodeType, groupID *string) ([]EpisodicNode,error) {
	if lastN==0{lastN=10}
	if groupID!=nil&&len(groupIDs)==0{groupIDs=[]string{*groupID}}
	clauses:=[]string{};params:=map[string]any{"n":lastN}
	if referenceTime!=nil{clauses=append(clauses,"reference_time <= $ref");params["ref"]=*referenceTime}
	if len(groupIDs)>0{clauses=append(clauses,"group_id IN $groups");params["groups"]=groupIDs}
	if source!=nil{clauses=append(clauses,"source = $source");params["source"]=string(*source)}
	where:="";if len(clauses)>0{where="WHERE "+strings.Join(clauses," AND ")}
	raw,err:=s.Driver.Query(ctx,"SELECT * FROM episode "+where+" ORDER BY reference_time DESC LIMIT $n;",params)
	if err!=nil{return nil,err}
	rows:=UnwrapRows(raw);out:=make([]EpisodicNode,0,len(rows))
	for _,row:=range rows{out=append(out,ParseEpisode(row))}
	return out,nil
}

func (s *Surriti) fetchEpisodeContents(ctx context.Context, episodeUUIDs []string)(string,error){
	if len(episodeUUIDs)==0{return "",nil}
	raw,err:=s.Driver.Query(ctx,"SELECT content FROM episode WHERE uuid IN $u;",map[string]any{"u":episodeUUIDs})
	if err!=nil{return "",err}
	parts:=[]string{}
	for _,row:=range UnwrapRows(raw){if text:=strings.TrimSpace(stringFromAny(row["content"]));text!=""{parts=append(parts,text)}}
	return strings.Join(parts,"\n---\n"),nil
}

func (s *Surriti) fetchRecentEpisodeContents(ctx context.Context,referenceTime time.Time,groupID string,source EpisodeType,limit int)(string,error){
	if limit==0{limit=10}
	episodes,err:=s.RetrieveEpisodes(ctx,&referenceTime,limit,nil,&source,&groupID)
	if err!=nil{return "",err}
	sort.SliceStable(episodes,func(i,j int)bool{return episodes[i].ReferenceTime.Before(episodes[j].ReferenceTime)})
	parts:=[]string{}
	for _,ep:=range episodes{if text:=strings.TrimSpace(ep.Content);text!=""{parts=append(parts,text)}}
	return strings.Join(parts,"\n---\n"),nil
}

func filterFactsByEdgeTypeMap(entities []ExtractedEntity,facts []ExtractedFact,rules map[EntityTypePair][]string)[]ExtractedFact{
	labelsByName:=map[string]map[string]struct{}{}
	for _,ent:=range entities{
		set:=map[string]struct{}{"Entity":{}}
		for _,label:=range ent.Labels{set[label]=struct{}{}}
		labelsByName[ent.Name]=set
	}
	out:=[]ExtractedFact{}
	for _,fact:=range facts{
		subj:=labelsByName[fact.Subject];if subj==nil{subj=map[string]struct{}{"Entity":{}}}
		obj:=labelsByName[fact.Object];if obj==nil{obj=map[string]struct{}{"Entity":{}}}
		allowed:=map[string]struct{}{};matched:=false
		for pair,predicates:=range rules{
			_,sok:=subj[pair.Source];_,ook:=obj[pair.Target]
			if !sok||!ook{continue}
			matched=true
			for _,p:=range predicates{allowed[p]=struct{}{}}
		}
		if !matched{continue}
		if len(allowed)==0{out=append(out,fact);continue}
		if _,ok:=allowed[fact.Predicate];ok{out=append(out,fact)}
	}
	return out
}

func speakerExtractionHint(speakerID string,speakerName *string)string{
	subject:=speakerID
	if speakerName!=nil&&*speakerName!=""{subject=*speakerName}
	return fmt.Sprintf(`(Speaker context: "I"/"me"/"my"/"mine" refer to %q (stable id %q). Use %q as the subject for facts about the speaker. The OBJECT must be the actual value mentioned in the text -- never the literal word "speaker" or the speaker again. CRITICAL -- third-party subjects: when the sentence's grammatical subject is a NAMED entity rather than a first-person pronoun, KEEP that named subject. Resolve third-person pronouns to the most recently mentioned named entity from CONTEXT, not to the speaker.)`,subject,speakerID,subject)
}

func pointerValue(p *string)string{if p==nil{return ""};return *p}

func (s *Surriti) AddEpisode(ctx context.Context,req AddEpisodeRequest)(AddEpisodeResults,error){
	refTime:=utcNow();if req.ReferenceTime!=nil{refTime=req.ReferenceTime.UTC()}
	source:=req.Source;if source==""{source=EpisodeMessage}
	sourceType:=req.SourceType;if sourceType==""{sourceType="user"}

	resumeExisting:=false
	if req.UUID!=nil&&*req.UUID!=""{
		raw,err:=s.Driver.Query(ctx,"SELECT uuid, ingestion_complete FROM episode WHERE uuid = $uuid AND group_id = $group_id LIMIT 1;",map[string]any{"uuid":*req.UUID,"group_id":req.GroupID})
		if err!=nil{return AddEpisodeResults{},err}
		rows:=UnwrapRows(raw)
		if len(rows)>0{
			if complete,ok:=rows[0]["ingestion_complete"].(bool);ok&&complete{
				ep:=NewEpisodicNode(req.Name,req.GroupID);ep.UUID=*req.UUID;ep.Content=req.EpisodeBody;ep.Source=source;ep.SourceDescription=req.SourceDescription;ep.ReferenceTime=refTime
				return AddEpisodeResults{Episode:ep,EpisodicEdges:[]EpisodicEdge{},Nodes:[]EntityNode{},Edges:[]EntityEdge{},InvalidatedEdges:[]EntityEdge{},Communities:[]CommunityNode{},CommunityEdges:[]CommunityEdge{}},nil
			}
			resumeExisting=true
		}
	}

	var extractionContext string;var err error
	if req.PreviousEpisodeUUIDs==nil{
		extractionContext,err=s.fetchRecentEpisodeContents(ctx,refTime,req.GroupID,source,10)
	}else{
		extractionContext,err=s.fetchEpisodeContents(ctx,req.PreviousEpisodeUUIDs)
	}
	if err!=nil{return AddEpisodeResults{},err}

	episode:=NewEpisodicNode(req.Name,req.GroupID)
	if req.UUID!=nil&&*req.UUID!=""{episode.UUID=*req.UUID}
	episode.Content=req.EpisodeBody;episode.Source=source;episode.SourceDescription=req.SourceDescription;episode.ReferenceTime=refTime
	if !resumeExisting{
		if err:=s.saveEpisode(ctx,episode);err!=nil{return AddEpisodeResults{},err}
	}

	var speakerEntity *EntityNode
	custom:=req.CustomExtractionInstructions
	if req.SpeakerID!=nil&&*req.SpeakerID!=""{
		entity,err:=s.UpsertUser(ctx,req.GroupID,*req.SpeakerID,pointerValue(req.SpeakerName),"")
		if err!=nil{return AddEpisodeResults{},err}
		speakerEntity=&entity
		hint:=speakerExtractionHint(*req.SpeakerID,req.SpeakerName)
		if custom!=""{custom=custom+"\n\n"+hint}else{custom=hint}
	}

	extraction,err:=s.LLM.Extract(ctx,ExtractionRequest{
		Content:req.EpisodeBody,GroupID:req.GroupID,EntityTypes:req.EntityTypes,
		CustomInstructions:custom,Context:extractionContext,
	})
	if err!=nil{return AddEpisodeResults{},err}

	if speakerEntity!=nil&&req.SpeakerID!=nil{
		keys:=map[string]struct{}{entityNameKey(*req.SpeakerID):{}}
		if req.SpeakerName!=nil&&*req.SpeakerName!=""{keys[entityNameKey(*req.SpeakerName)]=struct{}{}}
		for i:=range extraction.Facts{
			if _,ok:=keys[entityNameKey(extraction.Facts[i].Subject)];ok{extraction.Facts[i].Subject=speakerEntity.Name}
		}
		kept:=extraction.Entities[:0]
		for _,entity:=range extraction.Entities{if _,ok:=keys[entityNameKey(entity.Name)];!ok{kept=append(kept,entity)}}
		extraction.Entities=kept
	}

	if len(req.EntityTypes)>0{
		typeNames:=make([]string,0,len(req.EntityTypes));for name:=range req.EntityTypes{typeNames=append(typeNames,name)}
		for i:=range extraction.Entities{
			labels:=append([]string(nil),extraction.Entities[i].Labels...)
			lname:=strings.ToLower(extraction.Entities[i].Name)
			for _,t:=range typeNames{if strings.Contains(lname,strings.ToLower(t)){labels=append(labels,t)}}
			extraction.Entities[i].Labels=sortedStringsUnique(labels)
		}
	}
	if len(req.EdgeTypes)>0{
		allowed:=map[string]struct{}{};for p:=range req.EdgeTypes{allowed[p]=struct{}{}}
		kept:=extraction.Facts[:0]
		for _,fact:=range extraction.Facts{if fact.Predicate==""{kept=append(kept,fact);continue};if _,ok:=allowed[fact.Predicate];ok{kept=append(kept,fact)}}
		extraction.Facts=kept
	}
	if req.EdgeTypeMap!=nil{extraction.Facts=filterFactsByEdgeTypeMap(extraction.Entities,extraction.Facts,req.EdgeTypeMap)}
	if len(req.ExcludedEntityTypes)>0{
		excluded:=map[string]struct{}{};for _,v:=range req.ExcludedEntityTypes{excluded[v]=struct{}{}}
		entities:=extraction.Entities[:0];keepNames:=map[string]struct{}{}
		for _,entity:=range extraction.Entities{
			drop:=false;for _,label:=range entity.Labels{if _,ok:=excluded[label];ok{drop=true;break}}
			if !drop{entities=append(entities,entity);keepNames[entity.Name]=struct{}{}}
		}
		extraction.Entities=entities
		facts:=extraction.Facts[:0]
		for _,fact:=range extraction.Facts{_,sok:=keepNames[fact.Subject];_,ook:=keepNames[fact.Object];if sok&&ook{facts=append(facts,fact)}}
		extraction.Facts=facts
	}

	entities,err:=s.upsertEntities(ctx,extraction.Entities,req.GroupID,&episode.UUID,req.EpisodeBody)
	if err!=nil{return AddEpisodeResults{},err}
	entityByKey:=map[string]EntityNode{};nameToEntity:=map[string]EntityNode{}
	for _,entity:=range entities{entityByKey[entityNameKey(entity.Name)]=entity;nameToEntity[entity.Name]=entity}
	for _,ext:=range extraction.Entities{if stored,ok:=entityByKey[entityNameKey(ext.Name)];ok{nameToEntity[ext.Name]=stored}}

	if speakerEntity!=nil&&req.SpeakerID!=nil{
		nameToEntity[speakerEntity.Name]=*speakerEntity;nameToEntity[*req.SpeakerID]=*speakerEntity
		if req.SpeakerName!=nil&&*req.SpeakerName!=""{nameToEntity[*req.SpeakerName]=*speakerEntity}
		found:=false;for _,e:=range entities{if e.UUID==speakerEntity.UUID{found=true;break}}
		if !found{entities=append(entities,*speakerEntity)}
	}

	edges:=[]EntityEdge{};invalidatedAll:=[]EntityEdge{}
	for _,rawFact:=range extraction.Facts{
		originalSubject:=rawFact.Subject
		repaired:=RepairFact(rawFact,pointerValue(req.SpeakerID),pointerValue(req.SpeakerName))
		if repaired==nil{continue}
		fact:=*repaired
		if req.SpeakerID!=nil&&fact.Subject==*req.SpeakerID&&fact.Subject!=originalSubject{
			if _,ok:=nameToEntity[fact.Subject];!ok{
				speakerEnts,err:=s.upsertEntities(ctx,[]ExtractedEntity{{Name:*req.SpeakerID,Labels:[]string{"User"}}},req.GroupID,nil,"")
				if err!=nil{return AddEpisodeResults{},err}
				if len(speakerEnts)>0{nameToEntity[*req.SpeakerID]=speakerEnts[0];entities=appendUniqueEntity(entities,speakerEnts[0])}
			}
		}
		subject,sok:=nameToEntity[fact.Subject];obj,ook:=nameToEntity[fact.Object]
		if !sok||!ook{continue}
		if subject.UUID==obj.UUID{
			if _,ok:=IdentityPredicates[fact.Predicate];!ok{continue}
		}
		op:=fact.Operation;if op==""{op=FactAssert}
		if op==FactNoop{continue}
		if op==FactTerminate{
			closed,err:=s.terminateMatchingEdge(ctx,req.GroupID,subject.UUID,obj.UUID,fact.Predicate,refTime)
			if err!=nil{return AddEpisodeResults{},err}
			invalidatedAll=append(invalidatedAll,closed...)
			continue
		}
		if op==FactCorrect{fact.Singleton=true}
		edge,invalidated,err:=s.addFactEdge(ctx,fact,subject,obj,&episode,req.GroupID,sourceType)
		if err!=nil{return AddEpisodeResults{},err}
		if req.SpeakerID!=nil&&*req.SpeakerID!=""{
			if err:=s.writeMemoryRefsForFact(ctx,req.GroupID,edge.UUID,*req.SpeakerID,req.ParticipantIDs,episode.UUID,req.ConversationID,refTime);err!=nil{return AddEpisodeResults{},err}
		}
		edges=append(edges,edge);invalidatedAll=append(invalidatedAll,invalidated...)
	}

	if len(edges)>0{
		episode.EntityEdges=make([]string,len(edges));for i,e:=range edges{episode.EntityEdges[i]=e.UUID}
		if _,err:=s.Driver.Query(ctx,"UPDATE episode SET entity_edges = $ee WHERE uuid = $u;",map[string]any{"ee":episode.EntityEdges,"u":episode.UUID});err!=nil{return AddEpisodeResults{},err}
	}
	mentionEdges,err:=s.linkMentions(ctx,episode,entities,req.GroupID)
	if err!=nil{return AddEpisodeResults{},err}

	communities:=[]CommunityNode{};communityEdges:=[]CommunityEdge{}
	if req.UpdateCommunities{
		communities,communityEdges,err=s.BuildCommunities(ctx,req.GroupID)
		if err!=nil{return AddEpisodeResults{},err}
	}

	// Profile refresh is deliberately fail-soft: base ingest success never
	// depends on dossier enrichment.
	if len(entities)>0&&s.ProfileRefreshMode!="off"{
		ids:=make([]string,0,len(entities));for _,e:=range entities{if e.UUID!=""{ids=append(ids,e.UUID)}}
		if s.ProfileRefreshMode=="sync"{
			_ = RefreshEntityProfiles(ctx,s.Driver,s.Embedder,s.LLM,req.GroupID,ids,s.ProfileSummaryMaxFacts,800)
		}else if len(ids)>0{
			idsCopy:=append([]string(nil),ids...)
			s.runBackground(func(bg context.Context){
				_ = RefreshEntityProfiles(bg,s.Driver,s.Embedder,s.LLM,req.GroupID,idsCopy,s.ProfileSummaryMaxFacts,800)
			})
		}
	}

	// Notify cognition after the canonical graph is durable but before
	// ingestion_complete, exactly like Python. Restart recovery only picks up
	// completed episodes, while a live scheduler may begin immediately.
	if scheduler:=s.CognitionScheduler();scheduler!=nil&&scheduler.Enabled(){
		scheduler.Notify(req.GroupID,episode.UUID)
	}

	if _,err:=s.Driver.Query(ctx,"UPDATE episode SET ingestion_complete = true WHERE uuid = $uuid;",map[string]any{"uuid":episode.UUID});err!=nil{return AddEpisodeResults{},err}
	return AddEpisodeResults{Episode:episode,EpisodicEdges:mentionEdges,Nodes:entities,Edges:edges,InvalidatedEdges:invalidatedAll,Communities:communities,CommunityEdges:communityEdges},nil
}

func appendUniqueEntity(values []EntityNode,node EntityNode)[]EntityNode{
	for _,v:=range values{if v.UUID==node.UUID{return values}}
	return append(values,node)
}

type AddTripletRequest struct {
	SourceNode *EntityNode
	Edge *EntityEdge
	TargetNode *EntityNode
	SubjectName string
	Predicate string
	ObjectName string
	Fact string
	GroupID string
	ValidAt *time.Time
}

func (s *Surriti) AddTriplet(ctx context.Context,req AddTripletRequest)(AddTripletResults,error){
	groupID:=req.GroupID
	var subjectName,predicate,objectName,factText string
	ref:=utcNow();if req.ValidAt!=nil{ref=req.ValidAt.UTC()}
	if req.SourceNode!=nil&&req.TargetNode!=nil&&req.Edge!=nil{
		groupID=req.SourceNode.GroupID;if groupID==""{groupID=req.Edge.GroupID};if groupID==""{groupID=req.GroupID}
		entities,err:=s.upsertEntities(ctx,[]ExtractedEntity{
			{Name:req.SourceNode.Name,Summary:req.SourceNode.Summary,Labels:req.SourceNode.Labels},
			{Name:req.TargetNode.Name,Summary:req.TargetNode.Summary,Labels:req.TargetNode.Labels},
		},groupID,nil,"")
		if err!=nil{return AddTripletResults{},err}
		if len(entities)<2{return AddTripletResults{},fmt.Errorf("triplet entity upsert returned fewer than two entities")}
		if req.Edge.ValidAt!=nil{ref=req.Edge.ValidAt.UTC()}
		fact:=NewExtractedFact(entities[0].Name,req.Edge.Name,entities[1].Name)
		fact.Fact=req.Edge.Fact;if fact.Fact==""{fact.Fact=fmt.Sprintf("%s %s %s.",entities[0].Name,req.Edge.Name,entities[1].Name)}
		t:=ref.Format(time.RFC3339Nano);fact.ValidAt=&t
		edge,invalidated,err:=s.addFactEdge(ctx,fact,entities[0],entities[1],nil,groupID,"user")
		if err!=nil{return AddTripletResults{},err}
		return AddTripletResults{Nodes:entities,Edges:[]EntityEdge{edge},InvalidatedEdges:invalidated},nil
	}
	subjectName=req.SubjectName;predicate=req.Predicate;objectName=req.ObjectName
	if subjectName==""||predicate==""||objectName==""{return AddTripletResults{},fmt.Errorf("%w: add_triplet requires subject, predicate, and object",ErrConfig)}
	entities,err:=s.upsertEntities(ctx,[]ExtractedEntity{NewExtractedEntity(subjectName),NewExtractedEntity(objectName)},groupID,nil,"")
	if err!=nil{return AddTripletResults{},err}
	if len(entities)<2{return AddTripletResults{},fmt.Errorf("triplet entity upsert returned fewer than two entities")}
	factText=req.Fact;if factText==""{factText=fmt.Sprintf("%s %s %s.",subjectName,predicate,objectName)}
	fact:=NewExtractedFact(subjectName,predicate,objectName);fact.Fact=factText;t:=ref.Format(time.RFC3339Nano);fact.ValidAt=&t
	edge,invalidated,err:=s.addFactEdge(ctx,fact,entities[0],entities[1],nil,groupID,"user")
	if err!=nil{return AddTripletResults{},err}
	return AddTripletResults{Nodes:entities,Edges:[]EntityEdge{edge},InvalidatedEdges:invalidated},nil
}

func (s *Surriti) AddEpisodeBulk(ctx context.Context,episodes []RawEpisode,groupID string,updateCommunities bool)(AddBulkEpisodeResults,error){
	agg:=AddBulkEpisodeResults{Episodes:[]EpisodicNode{},EpisodicEdges:[]EpisodicEdge{},Nodes:[]EntityNode{},Edges:[]EntityEdge{},InvalidatedEdges:[]EntityEdge{},Communities:[]CommunityNode{},CommunityEdges:[]CommunityEdge{}}
	for _,ep:=range episodes{
		g:=groupID;if ep.GroupID!=nil{g=*ep.GroupID}
		res,err:=s.AddEpisode(ctx,AddEpisodeRequest{Name:ep.Name,EpisodeBody:ep.Content,Source:ep.Source,SourceDescription:ep.SourceDescription,ReferenceTime:ep.ReferenceTime,GroupID:g,UUID:ep.UUID})
		if err!=nil{return AddBulkEpisodeResults{},err}
		agg.Episodes=append(agg.Episodes,res.Episode);agg.EpisodicEdges=append(agg.EpisodicEdges,res.EpisodicEdges...)
		agg.Nodes=append(agg.Nodes,res.Nodes...);agg.Edges=append(agg.Edges,res.Edges...);agg.InvalidatedEdges=append(agg.InvalidatedEdges,res.InvalidatedEdges...)
	}
	if updateCommunities{
		var err error;agg.Communities,agg.CommunityEdges,err=s.BuildCommunities(ctx,groupID);if err!=nil{return AddBulkEpisodeResults{},err}
	}
	return agg,nil
}
