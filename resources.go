package surriti

import (
	"context"
	"time"
)

type Resource struct {
	LibraryItemID string    `json:"library_item_id"`
	Title         string    `json:"title"`
	Kind          string    `json:"kind"`
	Relationship  string    `json:"relationship"`
	Summary       string    `json:"summary"`
	Topics        []string  `json:"topics"`
	Available     bool      `json:"available"`
	UUID          string    `json:"uuid"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func NewResource(libraryItemID, title string) Resource {
	now := utcNow()
	return Resource{
		LibraryItemID: libraryItemID,
		Title:         title,
		Kind:          "document",
		Relationship:  "reference_material",
		Topics:        []string{},
		Available:     true,
		UUID:          newUUID(),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

type ResourceStore struct{ driver Queryer }

func NewResourceStore(driver Queryer) *ResourceStore {
	return &ResourceStore{driver: driver}
}

func (s *ResourceStore) Upsert(ctx context.Context, resource Resource, groupID string) (Resource, error) {
	now := utcNow()
	payload := map[string]any{
		"library_item_id": resource.LibraryItemID,
		"title":           resource.Title,
		"kind":            resource.Kind,
		"relationship":    resource.Relationship,
		"summary":         resource.Summary,
		"topics":          append([]string(nil), resource.Topics...),
		"available":       resource.Available,
		"uuid":            resource.UUID,
		"created_at":      resource.CreatedAt,
		"updated_at":      now,
		"group_id":        groupID,
	}
	rows, err := s.driver.Query(ctx,
		"SELECT uuid, created_at FROM resource WHERE group_id = $group_id AND library_item_id = $library_item_id LIMIT 1;",
		payload,
	)
	if err != nil {
		return resource, err
	}
	existing := UnwrapRows(rows)
	if len(existing) > 0 {
		resource.UUID = stringFromAny(existing[0]["uuid"])
		if created := asTimePtr(existing[0]["created_at"]); created != nil {
			resource.CreatedAt = *created
		}
		payload["uuid"] = resource.UUID
		payload["created_at"] = resource.CreatedAt
	}
	if _, err := s.driver.Query(ctx,
		`UPSERT type::record("resource", $uuid) CONTENT $resource;`,
		map[string]any{"uuid": resource.UUID, "resource": payload},
	); err != nil {
		return resource, err
	}
	resource.UpdatedAt = now
	return resource, nil
}

func (s *ResourceStore) SetAvailability(ctx context.Context, libraryItemID, groupID string, available bool) (bool, error) {
	rows, err := s.driver.Query(ctx,
		"UPDATE resource SET available = $available, updated_at = time::now() WHERE group_id = $group_id AND library_item_id = $library_item_id RETURN AFTER;",
		map[string]any{"group_id": groupID, "library_item_id": libraryItemID, "available": available},
	)
	if err != nil {
		return false, err
	}
	return len(UnwrapRows(rows)) > 0, nil
}

func (s *ResourceStore) List(ctx context.Context, groupID string, limit int) ([]Resource, error) {
	if limit == 0 {
		limit = 100
	}
	rows, err := s.driver.Query(ctx,
		"SELECT * FROM resource WHERE group_id = $group_id AND available = true LIMIT $limit;",
		map[string]any{"group_id": groupID, "limit": limit},
	)
	if err != nil {
		return nil, err
	}
	out := make([]Resource, 0)
	for _, row := range UnwrapRows(rows) {
		r := NewResource(stringFromAny(row["library_item_id"]), stringFromAny(row["title"]))
		r.Kind = stringFromAny(row["kind"])
		if r.Kind == "" {
			r.Kind = "document"
		}
		r.Relationship = stringFromAny(row["relationship"])
		if r.Relationship == "" {
			r.Relationship = "reference_material"
		}
		r.Summary = stringFromAny(row["summary"])
		r.Topics = asStringSlice(row["topics"])
		if r.Topics == nil {
			r.Topics = []string{}
		}
		if v, ok := row["available"].(bool); ok {
			r.Available = v
		}
		if v := stringFromAny(row["uuid"]); v != "" {
			r.UUID = v
		}
		if v := asTimePtr(row["created_at"]); v != nil {
			r.CreatedAt = v.UTC()
		}
		if v := asTimePtr(row["updated_at"]); v != nil {
			r.UpdatedAt = v.UTC()
		}
		out = append(out, r)
	}
	return out, nil
}

func (s *Surriti) UpsertResource(ctx context.Context, resource Resource, groupID string) (Resource, error) {
	return s.Resources.Upsert(ctx, resource, groupID)
}

func (s *Surriti) ListResources(ctx context.Context, groupID string, limit int) ([]Resource, error) {
	return s.Resources.List(ctx, groupID, limit)
}

func (s *Surriti) SetResourceAvailability(ctx context.Context, libraryItemID, groupID string, available bool) (bool, error) {
	return s.Resources.SetAvailability(ctx, libraryItemID, groupID, available)
}
