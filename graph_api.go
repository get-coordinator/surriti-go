package surriti

import (
	"context"
	"os"
	"strings"
)

func (s *Surriti) GetEntityNode(ctx context.Context, uuid string) (*EntityNode, error) {
	raw, err := s.Driver.Query(ctx, "SELECT * FROM entity WHERE uuid = $u LIMIT 1;", map[string]any{"u": uuid})
	if err != nil {
		return nil, err
	}
	rows := UnwrapRows(raw)
	if len(rows) == 0 {
		return nil, nil
	}
	n := ParseEntity(rows[0])
	return &n, nil
}

func (s *Surriti) GetEntityEdge(ctx context.Context, uuid string) (*EntityEdge, error) {
	raw, err := s.Driver.Query(ctx, "SELECT * FROM relates_to WHERE uuid = $u LIMIT 1;", map[string]any{"u": uuid})
	if err != nil {
		return nil, err
	}
	rows := UnwrapRows(raw)
	if len(rows) == 0 {
		return nil, nil
	}
	e := ParseEdge(rows[0])
	return &e, nil
}

func (s *Surriti) GetEpisode(ctx context.Context, uuid string) (*EpisodicNode, error) {
	raw, err := s.Driver.Query(ctx, "SELECT * FROM episode WHERE uuid = $u LIMIT 1;", map[string]any{"u": uuid})
	if err != nil {
		return nil, err
	}
	rows := UnwrapRows(raw)
	if len(rows) == 0 {
		return nil, nil
	}
	e := ParseEpisode(rows[0])
	return &e, nil
}

func (s *Surriti) SaveNode(ctx context.Context, node EntityNode) (EntityNode, error) {
	if len(node.NameEmbedding) == 0 {
		emb, err := s.Embedder.Create(ctx, node.Name)
		if err != nil {
			return EntityNode{}, err
		}
		node.NameEmbedding = emb
	}
	_, err := s.Driver.Query(ctx, `
UPSERT type::record("entity", $uuid) MERGE {
 uuid: $uuid, group_id: $group_id, name: $name,
 summary: $summary, labels: $labels, attributes: $attributes,
 name_embedding: $emb, created_at: $created_at
};`, map[string]any{"uuid": node.UUID, "group_id": node.GroupID, "name": node.Name, "summary": node.Summary, "labels": node.Labels, "attributes": node.Attributes, "emb": node.NameEmbedding, "created_at": node.CreatedAt})
	return node, err
}

func (s *Surriti) SaveEdge(ctx context.Context, edge EntityEdge) (EntityEdge, error) {
	if len(edge.FactEmbedding) == 0 && edge.Fact != "" {
		emb, err := s.Embedder.Create(ctx, edge.Fact)
		if err != nil {
			return EntityEdge{}, err
		}
		edge.FactEmbedding = emb
	}
	_, err := s.Driver.Query(ctx, `
UPDATE relates_to MERGE {
 name: $name, fact: $fact, fact_embedding: $emb,
 episodes: $episodes, valid_at: $valid_at,
 invalid_at: $invalid_at, expired_at: $expired_at,
 attributes: $attributes
} WHERE uuid = $uuid;`, map[string]any{"uuid": edge.UUID, "name": edge.Name, "fact": edge.Fact, "emb": edge.FactEmbedding, "episodes": edge.Episodes, "valid_at": edge.ValidAt, "invalid_at": edge.InvalidAt, "expired_at": edge.ExpiredAt, "attributes": edge.Attributes})
	return edge, err
}

func (s *Surriti) RemoveEdge(ctx context.Context, edgeUUID string) error {
	_, err := s.Driver.Query(ctx, `
DELETE memory_ref WHERE out IN (SELECT VALUE id FROM relates_to WHERE uuid = $u);
DELETE relates_to WHERE uuid = $u;`, map[string]any{"u": edgeUUID})
	return err
}

func (s *Surriti) RemoveEpisode(ctx context.Context, episodeUUID string) error {
	raw, err := s.Driver.Query(ctx, "SELECT * FROM relates_to WHERE $ep IN episodes;", map[string]any{"ep": episodeUUID})
	if err != nil {
		return err
	}
	sole := []string{}
	shared := []string{}
	for _, row := range UnwrapRows(raw) {
		uid := stringFromAny(row["uuid"])
		eps := asStringSlice(row["episodes"])
		if len(eps) == 1 && eps[0] == episodeUUID {
			sole = append(sole, uid)
		} else {
			shared = append(shared, uid)
		}
	}
	if len(sole) > 0 {
		if _, err := s.Driver.Query(ctx, "DELETE memory_ref WHERE record::id(out) IN $u;", map[string]any{"u": sole}); err != nil {
			return err
		}
		if _, err := s.Driver.Query(ctx, "DELETE relates_to WHERE uuid IN $u;", map[string]any{"u": sole}); err != nil {
			return err
		}
	}
	if len(shared) > 0 {
		if _, err := s.Driver.Query(ctx, `UPDATE relates_to SET episodes = array::filter(episodes, |$x| $x != $ep) WHERE uuid IN $u;`, map[string]any{"u": shared, "ep": episodeUUID}); err != nil {
			return err
		}
	}
	if _, err := s.Driver.Query(ctx, "DELETE mentions WHERE in = type::record('episode', $ep);", map[string]any{"ep": episodeUUID}); err != nil {
		return err
	}
	_, err = s.Driver.Query(ctx, "DELETE episode WHERE uuid = $ep;", map[string]any{"ep": episodeUUID})
	return err
}

func destructiveAllowed() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("SURRITI_ALLOW_DESTRUCTIVE")))
	return v == "1" || v == "true" || v == "yes"
}

func (s *Surriti) DeleteGroup(ctx context.Context, groupID string) error {
	if !destructiveAllowed() {
		return &destructiveDisabledError{operation: "Surriti.delete_group()"}
	}
	for _, table := range []string{"memory_ref", "mentions", "relates_to", "has_member", "episode", "entity", "community"} {
		if _, err := s.Driver.Query(ctx, "DELETE "+table+" WHERE group_id = $g;", map[string]any{"g": groupID}); err != nil {
			return err
		}
	}
	return nil
}

type destructiveDisabledError struct{ operation string }

func (e *destructiveDisabledError) Error() string {
	return e.operation + " is disabled; set SURRITI_ALLOW_DESTRUCTIVE=1 to allow destructive operations"
}

func (s *Surriti) RemoveNode(ctx context.Context, entityUUID string) error {
	_, err := s.Driver.Query(ctx, `
DELETE memory_ref WHERE in = type::record('entity', $u)
 OR out IN (SELECT VALUE id FROM relates_to WHERE in = type::record('entity', $u) OR out = type::record('entity', $u));
DELETE relates_to WHERE in = type::record('entity', $u) OR out = type::record('entity', $u);
DELETE mentions WHERE out = type::record('entity', $u);
DELETE has_member WHERE out = type::record('entity', $u);
DELETE entity WHERE uuid = $u;`, map[string]any{"u": entityUUID})
	return err
}
