package surriti

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func (s *Surriti) ForgetMemoryForParticipant(ctx context.Context, groupID, participantID, factUUID string, role *string, invalidAt *time.Time) (int, error) {
	at := utcNow()
	if invalidAt != nil {
		at = invalidAt.UTC()
	}
	params := map[string]any{"group_id": groupID, "viewer_id": participantID, "fact_uuid": factUUID, "invalid_at": at}
	roleClause := ""
	if role != nil && *role != "" {
		roleClause = " AND role = $role"
		params["role"] = *role
	}
	query := "UPDATE memory_ref SET invalid_at = $invalid_at " +
		"WHERE ($group_id = \"\" OR group_id = $group_id) " +
		"AND viewer_id = $viewer_id " +
		"AND fact_uuid = $fact_uuid " +
		"AND invalid_at IS NONE" + roleClause + " RETURN AFTER;"
	raw, err := s.Driver.Query(ctx, query, params)
	if err != nil {
		return 0, err
	}
	return len(UnwrapRows(raw)), nil
}

func (s *Surriti) FilterFactsForParticipant(ctx context.Context, groupID, participantID string, facts []EntityEdge) ([]EntityEdge, map[string][]map[string]any, error) {
	if len(facts) == 0 {
		return []EntityEdge{}, map[string][]map[string]any{}, nil
	}
	uuids := []string{}
	for _, f := range facts {
		if f.UUID != "" {
			uuids = append(uuids, f.UUID)
		}
	}
	if len(uuids) == 0 {
		return []EntityEdge{}, map[string][]map[string]any{}, nil
	}
	raw, err := s.Driver.Query(ctx, `
SELECT uuid, role, source_actor_uuid, episode_uuid, conversation_id,
       valid_at, invalid_at, fact_uuid
FROM memory_ref
WHERE group_id = $group_id
 AND viewer_id = $participant_id
 AND fact_uuid IN $fact_uuids
 AND invalid_at IS NONE;`, map[string]any{"group_id": groupID, "participant_id": participantID, "fact_uuids": uuids})
	if err != nil {
		return nil, nil, err
	}
	refs := map[string][]map[string]any{}
	for _, row := range UnwrapRows(raw) {
		id := stringFromAny(row["fact_uuid"])
		if id != "" {
			refs[id] = append(refs[id], row)
		}
	}
	allowed := []EntityEdge{}
	for _, fact := range facts {
		if _, ok := refs[fact.UUID]; ok {
			allowed = append(allowed, fact)
		}
	}
	return allowed, refs, nil
}

func (s *Surriti) RecallForParticipant(ctx context.Context, query, participantID string, depth string, limit int, includeInvalid bool) (SearchResults, error) {
	_ = depth
	raw, err := s.Driver.Query(ctx, `
SELECT group_id, role, source_actor_uuid, episode_uuid, conversation_id,
       valid_at, fact_uuid
FROM memory_ref WHERE viewer_id = $viewer_id
 AND invalid_at IS NONE;`, map[string]any{"viewer_id": participantID})
	if err != nil {
		return SearchResults{}, err
	}
	refs := UnwrapRows(raw)
	allowed := []string{}
	seen := map[string]struct{}{}
	for _, row := range refs {
		id := stringFromAny(row["fact_uuid"])
		if id != "" {
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				allowed = append(allowed, id)
			}
		}
	}
	if len(allowed) == 0 {
		return SearchResults{Edges: []EntityEdge{}, Nodes: []EntityNode{}, Episodes: []EpisodicNode{}, Communities: []CommunityNode{}, Scores: map[string]float64{}}, nil
	}
	cfg := DefaultSearchConfig()
	if limit != 0 {
		cfg.Limit = limit
	}
	cfg.OnlyValid = !includeInvalid
	var emb []float64
	if query != "" {
		emb, err = s.Embedder.Create(ctx, query)
		if err != nil {
			return SearchResults{}, err
		}
	}
	result, err := HybridSearch(ctx, s.Driver, query, emb, nil, &cfg, nil, allowed)
	if err != nil {
		return SearchResults{}, err
	}
	byFact := map[string][]map[string]any{}
	for _, ref := range refs {
		id := stringFromAny(ref["fact_uuid"])
		byFact[id] = append(byFact[id], ref)
	}
	for i := range result.Edges {
		meta := byFact[result.Edges[i].UUID]
		result.Edges[i].ReferenceMetadata = meta
		roles := []string{}
		roleSeen := map[string]struct{}{}
		for _, r := range meta {
			role := stringFromAny(r["role"])
			if _, ok := roleSeen[role]; !ok {
				roleSeen[role] = struct{}{}
				roles = append(roles, role)
			}
		}
		result.Edges[i].ReferenceRoles = roles
	}
	return result, nil
}

func (s *Surriti) GrantMemoryAuthorized(ctx context.Context, sourceUserID, factUUID string) (bool, error) {
	raw, err := s.Driver.Query(ctx, `SELECT uuid FROM memory_ref WHERE viewer_id = $source AND role = 'asserted' AND invalid_at IS NONE AND fact_uuid = $fact_uuid LIMIT 1;`, map[string]any{"source": sourceUserID, "fact_uuid": factUUID})
	if err != nil {
		return false, err
	}
	return len(UnwrapRows(raw)) > 0, nil
}

func (s *Surriti) GrantMemoryToParticipant(ctx context.Context, sourceUserID, recipientUserID, factUUID string) (bool, error) {
	raw, err := s.Driver.Query(ctx, `
SELECT group_id, source_actor_uuid, episode_uuid, conversation_id, valid_at
FROM memory_ref WHERE viewer_id = $source AND role = 'asserted'
 AND invalid_at IS NONE AND fact_uuid = $fact_uuid LIMIT 1;`, map[string]any{"source": sourceUserID, "fact_uuid": factUUID})
	if err != nil {
		return false, err
	}
	rows := UnwrapRows(raw)
	if len(rows) == 0 {
		return false, nil
	}
	row := rows[0]
	groupID := stringFromAny(row["group_id"])
	recipient, err := s.UpsertUser(ctx, groupID, recipientUserID, "", "")
	if err != nil {
		return false, err
	}
	episodeUUID := stringFromAny(row["episode_uuid"])
	if episodeUUID == "" {
		episodeUUID = "grant:" + factUUID
	}
	var sourceActor *string
	if v := stringFromAny(row["source_actor_uuid"]); v != "" {
		sourceActor = &v
	}
	var conversation *string
	if v := stringFromAny(row["conversation_id"]); v != "" {
		conversation = &v
	}
	validAt := utcNow()
	if err := s.upsertMemoryRef(ctx, groupID, recipient.UUID, recipientUserID, factUUID, "granted", sourceActor, episodeUUID, conversation, validAt); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Surriti) RevokeMemoryFromParticipant(ctx context.Context, sourceUserID, recipientUserID, factUUID string) (int, error) {
	ok, err := s.GrantMemoryAuthorized(ctx, sourceUserID, factUUID)
	if err != nil || !ok {
		return 0, err
	}
	role := "granted"
	return s.ForgetMemoryForParticipant(ctx, "", recipientUserID, factUUID, &role, nil)
}

func (s *Surriti) upsertMemoryRef(ctx context.Context, groupID, actorUUID, viewerID, factUUID, role string, sourceActorUUID *string, episodeUUID string, conversationID *string, validAt time.Time) error {
	switch role {
	case "asserted", "witnessed", "granted", "endorsed", "disputed":
	default:
		return fmt.Errorf("%w: unsupported memory_ref role %q", ErrConfig, role)
	}
	refUUID := MakeMemoryRefKey(groupID, actorUUID, factUUID, role, episodeUUID)
	payload := map[string]any{"uuid": refUUID, "group_id": groupID, "viewer_id": viewerID, "fact_uuid": factUUID, "role": role, "source_actor_uuid": sourceActorUUID, "episode_uuid": episodeUUID, "conversation_id": conversationID, "valid_at": validAt, "invalid_at": nil, "created_at": utcNow()}
	raw, err := s.Driver.Query(ctx, "SELECT uuid FROM memory_ref WHERE uuid = $uuid LIMIT 1;", map[string]any{"uuid": refUUID})
	if err != nil {
		return err
	}
	update := func() error {
		_, err := s.Driver.Query(ctx, `
UPDATE memory_ref SET
 group_id=$group_id, viewer_id=$viewer_id, fact_uuid=$fact_uuid, role=$role,
 source_actor_uuid=$source_actor_uuid, episode_uuid=$episode_uuid,
 conversation_id=$conversation_id, valid_at=$valid_at, invalid_at=$invalid_at
WHERE uuid=$uuid;`, payload)
		return err
	}
	if len(UnwrapRows(raw)) > 0 {
		return update()
	}
	insert := cloneMap(payload)
	insert["actor_uuid"] = actorUUID
	_, err = s.Driver.Query(ctx, `
INSERT RELATION INTO memory_ref {
 in: type::record("entity", $actor_uuid),
 out: (SELECT VALUE id FROM relates_to WHERE uuid = $fact_uuid LIMIT 1)[0],
 uuid: $uuid, group_id: $group_id, viewer_id: $viewer_id, fact_uuid: $fact_uuid,
 role: $role, source_actor_uuid: $source_actor_uuid, episode_uuid: $episode_uuid,
 conversation_id: $conversation_id, valid_at: $valid_at, invalid_at: $invalid_at,
 created_at: $created_at
};`, insert)
	if err == nil {
		return nil
	}
	if !strings.Contains(err.Error(), "memory_ref_uuid_idx") {
		return err
	}
	return update()
}

func (s *Surriti) writeMemoryRefsForFact(ctx context.Context, groupID, factUUID, speakerID string, participantIDs []string, episodeUUID string, conversationID *string, validAt time.Time) error {
	speaker, err := s.UpsertUser(ctx, groupID, speakerID, "", "")
	if err != nil {
		return err
	}
	seen := map[string]struct{}{}
	ensure := func(userID, role string) error {
		if userID == "" {
			return nil
		}
		if _, ok := seen[userID]; ok {
			return nil
		}
		seen[userID] = struct{}{}
		actor := speaker
		if userID != speakerID {
			actor, err = s.UpsertUser(ctx, groupID, userID, "", "")
			if err != nil {
				return err
			}
		}
		source := speaker.UUID
		return s.upsertMemoryRef(ctx, groupID, actor.UUID, userID, factUUID, role, &source, episodeUUID, conversationID, validAt)
	}
	if err := ensure(speakerID, "asserted"); err != nil {
		return err
	}
	for _, id := range participantIDs {
		role := "witnessed"
		if id == speakerID {
			role = "asserted"
		}
		if err := ensure(id, role); err != nil {
			return err
		}
	}
	return nil
}
