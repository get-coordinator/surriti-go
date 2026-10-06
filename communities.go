package surriti

import (
	"context"
	"sort"
	"strings"
)

func (s *Surriti) BuildCommunities(ctx context.Context,groupID string)([]CommunityNode,[]CommunityEdge,error){
	if _,err:=s.Driver.Query(ctx,"DELETE has_member WHERE group_id = $g;",map[string]any{"g":groupID});err!=nil{return nil,nil,err}
	if _,err:=s.Driver.Query(ctx,"DELETE community WHERE group_id = $g;",map[string]any{"g":groupID});err!=nil{return nil,nil,err}
	rawNodes,err:=s.Driver.Query(ctx,"SELECT * FROM entity WHERE group_id = $g;",map[string]any{"g":groupID});if err!=nil{return nil,nil,err}
	rawEdges,err:=s.Driver.Query(ctx,`
SELECT record::id(in) AS source_uuid, record::id(out) AS target_uuid
FROM relates_to
WHERE group_id = $g AND invalid_at IS NONE;`,map[string]any{"g":groupID});if err!=nil{return nil,nil,err}
	nodes:=UnwrapRows(rawNodes);edges:=UnwrapRows(rawEdges)
	if len(nodes)==0{return []CommunityNode{},[]CommunityEdge{},nil}

	parent:=map[string]string{}
	for _,n:=range nodes{u:=stringFromAny(n["uuid"]);if u!=""{parent[u]=u}}
	var find func(string)string
	find=func(x string)string{
		for parent[x]!=x{
			parent[x]=parent[parent[x]]
			x=parent[x]
		}
		return x
	}
	union:=func(a,b string){
		ra,rb:=find(a),find(b);if ra!=rb{parent[ra]=rb}
	}
	degree:=map[string]int{}
	for _,e:=range edges{
		a,b:=stringFromAny(e["source_uuid"]),stringFromAny(e["target_uuid"])
		if _,ok:=parent[a];!ok{continue};if _,ok:=parent[b];!ok{continue}
		union(a,b);degree[a]++;degree[b]++
	}
	clusters:=map[string][]map[string]any{};rootOrder:=[]string{}
	for _,n:=range nodes{
		u:=stringFromAny(n["uuid"]);if _,ok:=parent[u];!ok{continue}
		root:=find(u)
		if _,ok:=clusters[root];!ok{rootOrder=append(rootOrder,root)}
		clusters[root]=append(clusters[root],n)
	}
	communities:=[]CommunityNode{};communityEdges:=[]CommunityEdge{}
	for _,root:=range rootOrder{
		members:=clusters[root];if len(members)<2{continue}
		head:=members[0];best:=degree[stringFromAny(head["uuid"])]
		for _,m:=range members[1:]{
			d:=degree[stringFromAny(m["uuid"])]
			if d>best{head=m;best=d}
		}
		names:=make([]string,0,len(members));for _,m:=range members{names=append(names,stringFromAny(m["name"]))};sort.Strings(names)
		headName:=stringFromAny(head["name"])
		embedding,err:=s.Embedder.Create(ctx,headName);if err!=nil{return nil,nil,err}
		community:=NewCommunityNode(headName+" community",groupID)
		community.Summary="Members: "+strings.Join(names,", ")
		community.NameEmbedding=embedding
		_,err=s.Driver.Query(ctx,`
CREATE type::record("community", $uuid) CONTENT {
 uuid: $uuid, group_id: $group_id, name: $name,
 summary: $summary, name_embedding: $emb, created_at: $created_at
};`,map[string]any{"uuid":community.UUID,"group_id":groupID,"name":community.Name,"summary":community.Summary,"emb":community.NameEmbedding,"created_at":community.CreatedAt})
		if err!=nil{return nil,nil,err}
		communities=append(communities,community)
		for _,m:=range members{
			ce:=CommunityEdge{EdgeBase:EdgeBase{BaseModel:NewBaseModel(groupID),SourceNodeUUID:community.UUID,TargetNodeUUID:stringFromAny(m["uuid"])}}
			_,err=s.Driver.Query(ctx,`
RELATE (type::record("community", $c))->has_member->(type::record("entity", $e))
CONTENT { uuid: $uuid, group_id: $group_id, created_at: $created_at };`,map[string]any{"c":community.UUID,"e":ce.TargetNodeUUID,"uuid":ce.UUID,"group_id":groupID,"created_at":ce.CreatedAt})
			if err!=nil{return nil,nil,err}
			communityEdges=append(communityEdges,ce)
		}
	}
	return communities,communityEdges,nil
}
