package surriti

import (
	"context"
	"encoding/json"
	"reflect"
)

// Persist frame identity before a fact references it. The cache only avoids
// unchanged writes; the database remains authoritative across client restarts.
func (s *Surriti) persistFrame(ctx context.Context, frame RelationFrame) (RelationFrame, error) {
	s.frameMu.Lock()
	defer s.frameMu.Unlock()
	key := frame.GroupID + "\x00" + frame.CanonicalName
	if old, ok := s.savedFrames[key]; ok && reflect.DeepEqual(old, frame) {
		return frame, nil
	}
	values := map[string]any{"uuid": frame.UUID, "group_id": frame.GroupID, "canonical_name": frame.CanonicalName, "aliases": frame.Aliases, "description": frame.Description, "directionality": string(frame.Directionality), "temporal_kind": string(frame.TemporalKind), "cardinality": string(frame.Cardinality), "contradiction_policy": string(frame.ContradictionPolicy), "inverse_name": frame.InverseName, "subject_role": frame.SubjectRole, "object_role": frame.ObjectRole, "confidence": frame.Confidence, "created_at": frame.CreatedAt}
	raw, err := s.Driver.Query(ctx, `UPSERT relation_frame SET
 uuid = uuid ?? $uuid, group_id = $group_id, canonical_name = $canonical_name,
 aliases = $aliases, description = $description, directionality = $directionality,
 temporal_kind = $temporal_kind, cardinality = $cardinality,
 contradiction_policy = $contradiction_policy, inverse_name = $inverse_name,
 subject_role = $subject_role, object_role = $object_role, confidence = $confidence,
 created_at = created_at ?? $created_at
 WHERE group_id = $group_id AND canonical_name = $canonical_name RETURN AFTER;`, values)
	if err != nil {
		return frame, err
	}
	if rows := UnwrapRows(raw); len(rows) > 0 {
		frame, err = parseStoredFrame(rows[0])
		if err != nil {
			return frame, err
		}
		s.RelationFrames.Register(frame, frame.GroupID)
	}
	if s.savedFrames == nil {
		s.savedFrames = map[string]RelationFrame{}
	}
	s.savedFrames[key] = cloneRelationFrame(frame)
	return frame, nil
}
func parseStoredFrame(row map[string]any) (RelationFrame, error) {
	var frame RelationFrame
	b, err := json.Marshal(row)
	if err != nil {
		return frame, err
	}
	err = json.Unmarshal(b, &frame)
	return frame, err
}
func (s *Surriti) loadFrames(ctx context.Context) error {
	raw, err := s.Driver.Query(ctx, "SELECT * FROM relation_frame;", nil)
	if err != nil {
		return err
	}
	for _, row := range UnwrapRows(raw) {
		frame, e := parseStoredFrame(row)
		if e != nil {
			return e
		}
		s.RelationFrames.Register(frame, frame.GroupID)
	}
	// Include explicitly configured group frames, not only the default catalog.
	s.RelationFrames.mu.RLock()
	groups := []string{""}
	for group := range s.RelationFrames.byGroup {
		groups = append(groups, group)
	}
	s.RelationFrames.mu.RUnlock()
	seen := map[string]bool{}
	for _, group := range groups {
		for _, frame := range s.RelationFrames.AllFrames(group) {
			key := frame.GroupID + "\x00" + frame.CanonicalName
			if seen[key] {
				continue
			}
			seen[key] = true
			if _, e := s.persistFrame(ctx, frame); e != nil {
				return e
			}
		}
	}
	return nil
}
