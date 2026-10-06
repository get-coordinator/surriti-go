package surriti

import (
	"encoding/json"
	"strings"
	"time"
)

func coerceTime(v any) *time.Time {
	if v == nil || v == "" {
		return nil
	}
	if t := asTimePtr(v); t != nil {
		u := t.UTC()
		return &u
	}
	return nil
}

func stringPtrFromAny(v any) *string {
	if v == nil {
		return nil
	}
	s := stringFromAny(v)
	return &s
}

func floatPtrFromAny(v any) *float64 {
	if v == nil {
		return nil
	}
	if f, ok := toFloat(v); ok {
		return &f
	}
	return nil
}

func boolFromAny(v any) bool {
	b, _ := v.(bool)
	return b
}

func rolesFromAny(v any) map[string]string {
	out := map[string]string{}
	for k, raw := range mapFromAny(v) {
		out[k] = stringFromAny(raw)
	}
	return out
}

func metadataSliceFromAny(v any) []map[string]any {
	switch x := v.(type) {
	case []map[string]any:
		return append([]map[string]any(nil), x...)
	case []any:
		out := make([]map[string]any, 0, len(x))
		for _, item := range x {
			if m, ok := toStringAnyMap(item); ok {
				out = append(out, m)
			}
		}
		return out
	default:
		return []map[string]any{}
	}
}

func ParseEpisode(row map[string]any) EpisodicNode {
	now := utcNow()
	uuid := stringFromAny(row["uuid"])
	if uuid == "" {
		uuid = stripRecordID(row["id"])
	}
	reference := now
	if v := coerceTime(row["reference_time"]); v != nil {
		reference = *v
	}
	created := now
	if v := coerceTime(row["created_at"]); v != nil {
		created = *v
	}
	source := stringFromAny(row["source"])
	if source == "" {
		source = string(EpisodeMessage)
	}
	affect := mapFromAny(row["affect"])
	if affect == nil {
		affect = map[string]any{}
	}
	edges := asStringSlice(row["entity_edges"])
	if edges == nil {
		edges = []string{}
	}
	return EpisodicNode{
		BaseModel: BaseModel{UUID: uuid, GroupID: stringFromAny(row["group_id"]), CreatedAt: created},
		Name:      stringFromAny(row["name"]), Source: EpisodeType(source),
		SourceDescription: stringFromAny(row["source_description"]),
		Content:           stringFromAny(row["content"]), ReferenceTime: reference, EntityEdges: edges,
		Affect: affect, InteractionPattern: stringPtrFromAny(row["interaction_pattern"]),
		CognitionProcessedAt: coerceTime(row["cognition_processed_at"]),
		CognitionVersion:     stringPtrFromAny(row["cognition_version"]),
	}
}

func ParseEntity(row map[string]any) EntityNode {
	now := utcNow()
	uuid := stringFromAny(row["uuid"])
	if uuid == "" {
		uuid = stripRecordID(row["id"])
	}
	created := now
	if v := coerceTime(row["created_at"]); v != nil {
		created = *v
	}
	labels := asStringSlice(row["labels"])
	if len(labels) == 0 {
		labels = []string{"Entity"}
	}
	aliases := asStringSlice(row["aliases"])
	if aliases == nil {
		aliases = []string{}
	}
	traits := asStringSlice(row["traits"])
	if traits == nil {
		traits = []string{}
	}
	goals := asStringSlice(row["goals_active"])
	if goals == nil {
		goals = []string{}
	}
	attrs := mapFromAny(row["attributes"])
	if attrs == nil {
		attrs = map[string]any{}
	}
	salience, _ := toFloat(row["salience"])
	mentionCount := intFromAny(row["mention_count"])
	return EntityNode{
		BaseModel: BaseModel{UUID: uuid, GroupID: stringFromAny(row["group_id"]), CreatedAt: created},
		Name:      stringFromAny(row["name"]), NameEmbedding: rowVector(row["name_embedding"]),
		Summary: stringFromAny(row["summary"]), Labels: labels, Attributes: attrs,
		CanonicalName: stringPtrFromAny(row["canonical_name"]), Aliases: aliases,
		ProfileSummary: stringFromAny(row["profile_summary"]), ProfileEmbedding: rowVector(row["profile_embedding"]),
		Salience: salience, MentionCount: mentionCount, LastSeenAt: coerceTime(row["last_seen_at"]),
		MergedInto: stringPtrFromAny(row["merged_into"]), Traits: traits, GoalsActive: goals,
		Domain: stringPtrFromAny(row["domain"]),
	}
}

func ParseEntityAlias(row map[string]any) EntityAlias {
	now := utcNow()
	uuid := stringFromAny(row["uuid"])
	if uuid == "" {
		uuid = stripRecordID(row["id"])
	}
	created := now
	if v := coerceTime(row["created_at"]); v != nil {
		created = *v
	}
	confidence := 1.0
	if row["confidence"] != nil {
		if v, ok := toFloat(row["confidence"]); ok {
			confidence = v
		}
	}
	return EntityAlias{
		BaseModel: BaseModel{UUID: uuid, GroupID: stringFromAny(row["group_id"]), CreatedAt: created},
		Alias:     stringFromAny(row["alias"]), NormalizedAlias: stringFromAny(row["normalized_alias"]),
		EntityUUID: stringFromAny(row["entity_uuid"]), Confidence: confidence,
		SourceEpisodeUUID: stringPtrFromAny(row["source_episode_uuid"]),
	}
}

func ParseEdge(row map[string]any) EntityEdge {
	now := utcNow()
	uuid := stringFromAny(row["uuid"])
	if uuid == "" {
		uuid = stripRecordID(row["id"])
	}
	created := now
	if v := coerceTime(row["created_at"]); v != nil {
		created = *v
	}
	src := stringFromAny(row["source_node_uuid"])
	if src == "" {
		src = stripRecordID(row["in"])
	}
	dst := stringFromAny(row["target_node_uuid"])
	if dst == "" {
		dst = stripRecordID(row["out"])
	}
	confidence := 1.0
	if row["confidence"] != nil {
		if v, ok := toFloat(row["confidence"]); ok {
			confidence = v
		}
	}
	weight := 1.0
	if row["weight"] != nil {
		if v, ok := toFloat(row["weight"]); ok {
			weight = v
		}
	}
	decayScore := 1.0
	if row["decay_score"] != nil {
		if v, ok := toFloat(row["decay_score"]); ok {
			decayScore = v
		}
	}
	reinforcement := intFromAny(row["reinforcement_count"])
	if reinforcement == 0 {
		reinforcement = 1
	}
	attrs := mapFromAny(row["attributes"])
	if attrs == nil {
		attrs = map[string]any{}
	}
	memoryClass := strings.ToLower(strings.TrimSpace(stringFromAny(attrs["memory_class"])))
	if memoryClass == "" {
		memoryClass = "objective"
	}
	status := stringFromAny(row["status"])
	if status == "" {
		status = "active"
	}
	polarity := stringFromAny(row["polarity"])
	if polarity == "" {
		polarity = "positive"
	}
	sourceType := stringFromAny(row["source_type"])
	if sourceType == "" {
		sourceType = "user"
	}
	stability := stringFromAny(row["stability"])
	if stability == "" {
		stability = "episodic"
	}
	episodes := asStringSlice(row["episodes"])
	if episodes == nil {
		episodes = []string{}
	}
	supersedes := asStringSlice(row["supersedes"])
	if supersedes == nil {
		supersedes = []string{}
	}
	consolidates := asStringSlice(row["consolidates"])
	if consolidates == nil {
		consolidates = []string{}
	}
	qualifiers := mapFromAny(row["qualifiers"])
	if qualifiers == nil {
		qualifiers = map[string]any{}
	}
	return EntityEdge{
		EdgeBase: EdgeBase{BaseModel: BaseModel{UUID: uuid, GroupID: stringFromAny(row["group_id"]), CreatedAt: created}, SourceNodeUUID: src, TargetNodeUUID: dst},
		Name:     stringFromAny(row["name"]), Fact: stringFromAny(row["fact"]), FactEmbedding: rowVector(row["fact_embedding"]),
		Episodes: episodes, ValidAt: coerceTime(row["valid_at"]), InvalidAt: coerceTime(row["invalid_at"]), ExpiredAt: coerceTime(row["expired_at"]),
		ReferenceRoles: asStringSlice(row["reference_roles"]), ReferenceMetadata: metadataSliceFromAny(row["reference_metadata"]),
		Status: status, Polarity: polarity, SourceType: sourceType, Confidence: confidence,
		Temporal: boolFromAny(row["temporal"]), Singleton: boolFromAny(row["singleton"]), Domain: stringPtrFromAny(row["domain"]),
		Supersedes: supersedes, SupersededBy: stringPtrFromAny(row["superseded_by"]), FactKey: stringFromAny(row["fact_key"]),
		RelationFrameID: stringPtrFromAny(row["relation_frame_id"]), CanonicalName: stringFromAny(row["canonical_name"]),
		Qualifiers: qualifiers, Roles: rolesFromAny(row["roles"]), ConflictGroupID: stringPtrFromAny(row["conflict_group_id"]),
		Derived: boolFromAny(row["derived"]), DerivedFrom: stringPtrFromAny(row["derived_from"]), MemoryClass: memoryClass,
		Weight: weight, ReinforcementCount: reinforcement, LastReinforcedAt: coerceTime(row["last_reinforced_at"]),
		RecallCount: intFromAny(row["recall_count"]), LastRecalledAt: coerceTime(row["last_recalled_at"]), DecayScore: decayScore,
		Stability: stability, Valence: floatPtrFromAny(row["valence"]), Intensity: floatPtrFromAny(row["intensity"]),
		Consolidates: consolidates, IsBelief: boolFromAny(row["is_belief"]), BeliefHolder: stringPtrFromAny(row["belief_holder"]), Attributes: attrs,
	}
}

func ParseEpisodicEdge(row map[string]any) EpisodicEdge {
	now := utcNow()
	uuid := stringFromAny(row["uuid"])
	if uuid == "" {
		uuid = stripRecordID(row["id"])
	}
	created := now
	if v := coerceTime(row["created_at"]); v != nil {
		created = *v
	}
	return EpisodicEdge{EdgeBase: EdgeBase{BaseModel: BaseModel{UUID: uuid, GroupID: stringFromAny(row["group_id"]), CreatedAt: created}, SourceNodeUUID: stripRecordID(row["in"]), TargetNodeUUID: stripRecordID(row["out"])}}
}

func ParseCommunityEdge(row map[string]any) CommunityEdge {
	now := utcNow()
	uuid := stringFromAny(row["uuid"])
	if uuid == "" {
		uuid = stripRecordID(row["id"])
	}
	created := now
	if v := coerceTime(row["created_at"]); v != nil {
		created = *v
	}
	return CommunityEdge{EdgeBase: EdgeBase{BaseModel: BaseModel{UUID: uuid, GroupID: stringFromAny(row["group_id"]), CreatedAt: created}, SourceNodeUUID: stripRecordID(row["in"]), TargetNodeUUID: stripRecordID(row["out"])}}
}

func ParseCommunity(row map[string]any) CommunityNode {
	now := utcNow()
	uuid := stringFromAny(row["uuid"])
	if uuid == "" {
		uuid = stripRecordID(row["id"])
	}
	created := now
	if v := coerceTime(row["created_at"]); v != nil {
		created = *v
	}
	kind := stringFromAny(row["kind"])
	if kind == "" {
		kind = "cluster"
	}
	payload := mapFromAny(row["payload"])
	if payload == nil {
		payload = map[string]any{}
	}
	return CommunityNode{
		BaseModel: BaseModel{UUID: uuid, GroupID: stringFromAny(row["group_id"]), CreatedAt: created},
		Name:      stringFromAny(row["name"]), NameEmbedding: rowVector(row["name_embedding"]),
		Summary: stringFromAny(row["summary"]), Kind: kind, Domain: stringPtrFromAny(row["domain"]), Payload: payload,
	}
}

func asStringSlice(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, v := range x {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func asTimePtr(v any) *time.Time {
	switch x := v.(type) {
	case time.Time:
		t := x
		return &t
	case *time.Time:
		return x
	case string:
		if t, err := time.Parse(time.RFC3339Nano, x); err == nil {
			return &t
		}
	}
	return nil
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case int:
		return float64(x), true
	case int8:
		return float64(x), true
	case int16:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint8:
		return float64(x), true
	case uint16:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	case float32:
		return float64(x), true
	case float64:
		return x, true
	default:
		return 0, false
	}
}
