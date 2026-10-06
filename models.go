package surriti

import (
	"strings"
	"time"
)

func utcNow() time.Time { return time.Now().UTC() }

type EpisodeType string

const (
	EpisodeMessage               EpisodeType = "message"
	EpisodeJSON                  EpisodeType = "json"
	EpisodeText                  EpisodeType = "text"
	EpisodeFactTriple            EpisodeType = "fact_triple"
	EpisodeSelfObservation       EpisodeType = "self_observation"
	EpisodeSelfCorrection        EpisodeType = "self_correction"
	EpisodeSelfSuccess           EpisodeType = "self_success"
	EpisodeSelfPattern           EpisodeType = "self_pattern"
	EpisodeToolFailure           EpisodeType = "tool_failure"
	EpisodeUserFrustration       EpisodeType = "user_frustration"
	EpisodeGoalCompletion        EpisodeType = "goal_completion"
	EpisodeHighConfidenceSuccess EpisodeType = "high_confidence_success"
	EpisodeAbandonment           EpisodeType = "abandonment"
)

type BaseModel struct {
	UUID      string    `json:"uuid"`
	GroupID   string    `json:"group_id"`
	CreatedAt time.Time `json:"created_at"`
}

func NewBaseModel(groupID string) BaseModel {
	return BaseModel{UUID: newUUID(), GroupID: groupID, CreatedAt: utcNow()}
}

type EpisodicNode struct {
	BaseModel
	Name                 string         `json:"name"`
	Source               EpisodeType    `json:"source"`
	SourceDescription    string         `json:"source_description"`
	Content              string         `json:"content"`
	ReferenceTime        time.Time      `json:"reference_time"`
	EntityEdges          []string       `json:"entity_edges"`
	Affect               map[string]any `json:"affect"`
	InteractionPattern   *string        `json:"interaction_pattern,omitempty"`
	CognitionProcessedAt *time.Time     `json:"cognition_processed_at,omitempty"`
	CognitionVersion     *string        `json:"cognition_version,omitempty"`
}

func NewEpisodicNode(name, groupID string) EpisodicNode {
	return EpisodicNode{
		BaseModel:     NewBaseModel(groupID),
		Name:          name,
		Source:        EpisodeMessage,
		ReferenceTime: utcNow(),
		EntityEdges:   []string{},
		Affect:        map[string]any{},
	}
}

type EntityNode struct {
	BaseModel
	Name             string         `json:"name"`
	NameEmbedding    []float64      `json:"name_embedding,omitempty"`
	Summary          string         `json:"summary"`
	Labels           []string       `json:"labels"`
	Attributes       map[string]any `json:"attributes"`
	CanonicalName    *string        `json:"canonical_name,omitempty"`
	Aliases          []string       `json:"aliases"`
	ProfileSummary   string         `json:"profile_summary"`
	ProfileEmbedding []float64      `json:"profile_embedding,omitempty"`
	Salience         float64        `json:"salience"`
	MentionCount     int            `json:"mention_count"`
	LastSeenAt       *time.Time     `json:"last_seen_at,omitempty"`
	MergedInto       *string        `json:"merged_into,omitempty"`
	Traits           []string       `json:"traits"`
	GoalsActive      []string       `json:"goals_active"`
	Domain           *string        `json:"domain,omitempty"`
}

func NewEntityNode(name, groupID string) EntityNode {
	return EntityNode{
		BaseModel:   NewBaseModel(groupID),
		Name:        name,
		Labels:      []string{"Entity"},
		Attributes:  map[string]any{},
		Aliases:     []string{},
		Traits:      []string{},
		GoalsActive: []string{},
	}
}

type EntityAlias struct {
	BaseModel
	Alias             string  `json:"alias"`
	NormalizedAlias   string  `json:"normalized_alias"`
	EntityUUID        string  `json:"entity_uuid"`
	Confidence        float64 `json:"confidence"`
	SourceEpisodeUUID *string `json:"source_episode_uuid,omitempty"`
}

type CommunityNode struct {
	BaseModel
	Name          string         `json:"name"`
	NameEmbedding []float64      `json:"name_embedding,omitempty"`
	Summary       string         `json:"summary"`
	Kind          string         `json:"kind"`
	Domain        *string        `json:"domain,omitempty"`
	Payload       map[string]any `json:"payload"`
}

func NewCommunityNode(name, groupID string) CommunityNode {
	return CommunityNode{
		BaseModel: NewBaseModel(groupID),
		Name:      name,
		Kind:      "cluster",
		Payload:   map[string]any{},
	}
}

type EdgeBase struct {
	BaseModel
	SourceNodeUUID string `json:"source_node_uuid"`
	TargetNodeUUID string `json:"target_node_uuid"`
}

type EpisodicEdge struct{ EdgeBase }

type EntityEdge struct {
	EdgeBase
	Name               string            `json:"name"`
	Fact               string            `json:"fact"`
	FactEmbedding      []float64         `json:"fact_embedding,omitempty"`
	Episodes           []string          `json:"episodes"`
	ValidAt            *time.Time        `json:"valid_at,omitempty"`
	InvalidAt          *time.Time        `json:"invalid_at,omitempty"`
	ExpiredAt          *time.Time        `json:"expired_at,omitempty"`
	ReferenceRoles     []string          `json:"reference_roles,omitempty"`
	ReferenceMetadata  []map[string]any  `json:"reference_metadata,omitempty"`
	Status             string            `json:"status"`
	Polarity           string            `json:"polarity"`
	SourceType         string            `json:"source_type"`
	Confidence         float64           `json:"confidence"`
	Temporal           bool              `json:"temporal"`
	Singleton          bool              `json:"singleton"`
	Domain             *string           `json:"domain,omitempty"`
	Supersedes         []string          `json:"supersedes"`
	SupersededBy       *string           `json:"superseded_by,omitempty"`
	FactKey            string            `json:"fact_key"`
	RelationFrameID    *string           `json:"relation_frame_id,omitempty"`
	CanonicalName      string            `json:"canonical_name"`
	Qualifiers         map[string]any    `json:"qualifiers"`
	Roles              map[string]string `json:"roles"`
	ConflictGroupID    *string           `json:"conflict_group_id,omitempty"`
	Derived            bool              `json:"derived"`
	DerivedFrom        *string           `json:"derived_from,omitempty"`
	MemoryClass        string            `json:"memory_class"`
	Weight             float64           `json:"weight"`
	ReinforcementCount int               `json:"reinforcement_count"`
	LastReinforcedAt   *time.Time        `json:"last_reinforced_at,omitempty"`
	RecallCount        int               `json:"recall_count"`
	LastRecalledAt     *time.Time        `json:"last_recalled_at,omitempty"`
	DecayScore         float64           `json:"decay_score"`
	Stability          string            `json:"stability"`
	Valence            *float64          `json:"valence,omitempty"`
	Intensity          *float64          `json:"intensity,omitempty"`
	Consolidates       []string          `json:"consolidates"`
	IsBelief           bool              `json:"is_belief"`
	BeliefHolder       *string           `json:"belief_holder,omitempty"`
	Attributes         map[string]any    `json:"attributes"`
}

func NewEntityEdge(sourceUUID, targetUUID, predicate, groupID string) EntityEdge {
	return EntityEdge{
		EdgeBase:           EdgeBase{BaseModel: NewBaseModel(groupID), SourceNodeUUID: sourceUUID, TargetNodeUUID: targetUUID},
		Name:               predicate,
		Episodes:           []string{},
		ReferenceRoles:     []string{},
		ReferenceMetadata:  []map[string]any{},
		Status:             "active",
		Polarity:           "positive",
		SourceType:         "user",
		Confidence:         1,
		Supersedes:         []string{},
		Qualifiers:         map[string]any{},
		Roles:              map[string]string{},
		MemoryClass:        "objective",
		Weight:             1,
		ReinforcementCount: 1,
		DecayScore:         1,
		Stability:          "episodic",
		Consolidates:       []string{},
		Attributes:         map[string]any{},
	}
}

type CommunityEdge struct{ EdgeBase }

func MakeFactKey(groupID, subjectUUID, predicate, objectUUID, qualifierHash string) string {
	parts := []string{strings.TrimSpace(groupID), strings.TrimSpace(subjectUUID), lowerTrim(predicate), strings.TrimSpace(objectUUID)}
	if qualifierHash != "" {
		parts = append(parts, qualifierHash)
	}
	return strings.Join(parts, "::")
}
