package surriti

import (
	"context"
	"fmt"
	"strings"
)

const schemaDDLTemplate = `
    -- Analyzers ---------------------------------------------------------
    DEFINE ANALYZER IF NOT EXISTS surriti_en
        TOKENIZERS blank,class,camel,punct
        FILTERS lowercase, ascii, snowball(english);

    -- Episode (raw input) ----------------------------------------------
    DEFINE TABLE IF NOT EXISTS episode SCHEMAFULL;
    DEFINE FIELD IF NOT EXISTS uuid              ON episode TYPE string;
    DEFINE FIELD IF NOT EXISTS group_id          ON episode TYPE string;
    DEFINE FIELD IF NOT EXISTS name              ON episode TYPE string;
    DEFINE FIELD IF NOT EXISTS source            ON episode TYPE string;
    DEFINE FIELD IF NOT EXISTS source_description ON episode TYPE string;
    DEFINE FIELD IF NOT EXISTS content           ON episode TYPE string;
    DEFINE FIELD IF NOT EXISTS reference_time    ON episode TYPE datetime;
    DEFINE FIELD IF NOT EXISTS created_at        ON episode TYPE datetime;
    DEFINE FIELD IF NOT EXISTS entity_edges      ON episode TYPE array<string> DEFAULT [];
    DEFINE FIELD IF NOT EXISTS ingestion_complete ON episode TYPE bool DEFAULT false;
    -- Cognitive layer (additive): per-episode affect tag and procedural
    -- interaction-pattern label. Both populated by ` + "`" + `` + "`" + `surriti.cognition` + "`" + `` + "`" + `;
    -- legacy rows simply read empty defaults.
    DEFINE FIELD IF NOT EXISTS affect            ON episode TYPE object FLEXIBLE DEFAULT {};
    DEFINE FIELD IF NOT EXISTS interaction_pattern ON episode TYPE option<string>;
    DEFINE FIELD IF NOT EXISTS cognition_processed_at ON episode TYPE option<datetime>;
    DEFINE FIELD IF NOT EXISTS cognition_version      ON episode TYPE option<string>;
    DEFINE INDEX IF NOT EXISTS episode_uuid_idx     ON episode FIELDS uuid UNIQUE;
    DEFINE INDEX IF NOT EXISTS episode_group_idx    ON episode FIELDS group_id;
    DEFINE INDEX IF NOT EXISTS episode_content_fts  ON episode FIELDS content
        FULLTEXT ANALYZER surriti_en BM25 HIGHLIGHTS;

    -- Entity ------------------------------------------------------------
    DEFINE TABLE IF NOT EXISTS entity SCHEMAFULL;
    DEFINE FIELD IF NOT EXISTS uuid           ON entity TYPE string;
    DEFINE FIELD IF NOT EXISTS group_id       ON entity TYPE string;
    DEFINE FIELD IF NOT EXISTS name           ON entity TYPE string;
    DEFINE FIELD IF NOT EXISTS summary        ON entity TYPE string DEFAULT "";
    DEFINE FIELD IF NOT EXISTS labels         ON entity TYPE array<string> DEFAULT ["Entity"];
    DEFINE FIELD IF NOT EXISTS attributes     ON entity TYPE object FLEXIBLE DEFAULT {};
    DEFINE FIELD IF NOT EXISTS name_embedding ON entity TYPE option<array<float>>;
    DEFINE FIELD IF NOT EXISTS created_at     ON entity TYPE datetime;
    -- Dossier / profile fields. All have safe defaults so existing rows
    -- migrate forward without backfill. ` + "`" + `` + "`" + `profiles.refresh_entity_profiles` + "`" + `` + "`" + `
    -- materialises the derived fields after each ingest.
    DEFINE FIELD IF NOT EXISTS canonical_name    ON entity TYPE option<string>;
    DEFINE FIELD IF NOT EXISTS aliases           ON entity TYPE array<string> DEFAULT [];
    DEFINE FIELD IF NOT EXISTS profile_summary   ON entity TYPE string DEFAULT "";
    DEFINE FIELD IF NOT EXISTS profile_embedding ON entity TYPE option<array<float>>;
    DEFINE FIELD OVERWRITE salience          ON entity TYPE option<float> DEFAULT 0;
    DEFINE FIELD OVERWRITE mention_count     ON entity TYPE option<int> DEFAULT 0;
    DEFINE FIELD IF NOT EXISTS last_seen_at      ON entity TYPE option<datetime>;
    DEFINE FIELD IF NOT EXISTS merged_into       ON entity TYPE option<string>;
    -- Cognitive layer (additive). All optional / cached / safe defaults.
    -- ` + "`" + `` + "`" + `traits` + "`" + `` + "`" + ` and ` + "`" + `` + "`" + `goals_active` + "`" + `` + "`" + ` are denormalised UUID lists kept in
    -- sync by ` + "`" + `` + "`" + `surriti.cognition` + "`" + `` + "`" + `; ` + "`" + `` + "`" + `domain` + "`" + `` + "`" + ` is the labelled cluster
    -- this entity belongs to (set by domain-aware community labelling).
    DEFINE FIELD IF NOT EXISTS traits            ON entity TYPE array<string> DEFAULT [];
    DEFINE FIELD IF NOT EXISTS goals_active      ON entity TYPE array<string> DEFAULT [];
    DEFINE FIELD IF NOT EXISTS domain            ON entity TYPE option<string>;
    DEFINE INDEX IF NOT EXISTS entity_uuid_idx     ON entity FIELDS uuid UNIQUE;
    DEFINE INDEX IF NOT EXISTS entity_group_idx    ON entity FIELDS group_id;
    DEFINE INDEX IF NOT EXISTS entity_name_uniq    ON entity FIELDS group_id, name UNIQUE;
    DEFINE INDEX IF NOT EXISTS entity_name_fts     ON entity FIELDS name
        FULLTEXT ANALYZER surriti_en BM25 HIGHLIGHTS;
    DEFINE INDEX IF NOT EXISTS entity_summary_fts  ON entity FIELDS summary
        FULLTEXT ANALYZER surriti_en BM25 HIGHLIGHTS;
    DEFINE INDEX IF NOT EXISTS entity_profile_fts  ON entity FIELDS profile_summary
        FULLTEXT ANALYZER surriti_en BM25 HIGHLIGHTS;
    DEFINE INDEX IF NOT EXISTS entity_name_hnsw    ON entity FIELDS name_embedding
        HNSW DIMENSION {{DIM}} DIST COSINE TYPE F32;
    DEFINE INDEX IF NOT EXISTS entity_profile_hnsw ON entity FIELDS profile_embedding
        HNSW DIMENSION {{DIM}} DIST COSINE TYPE F32;

    -- Entity aliases (canonical-resolution layer). Each row is a
    -- known surface form of an entity in a tenant. Lookup by
    -- ` + "`" + `` + "`" + `(group_id, normalized_alias)` + "`" + `` + "`" + ` is the fast path before any
    -- semantic / LLM resolution happens.
    DEFINE TABLE IF NOT EXISTS entity_alias SCHEMAFULL;
    DEFINE FIELD IF NOT EXISTS uuid                ON entity_alias TYPE string;
    DEFINE FIELD IF NOT EXISTS group_id            ON entity_alias TYPE string;
    DEFINE FIELD IF NOT EXISTS alias               ON entity_alias TYPE string;
    DEFINE FIELD IF NOT EXISTS normalized_alias    ON entity_alias TYPE string;
    DEFINE FIELD IF NOT EXISTS entity_uuid         ON entity_alias TYPE string;
    DEFINE FIELD IF NOT EXISTS confidence          ON entity_alias TYPE float DEFAULT 1.0;
    DEFINE FIELD IF NOT EXISTS source_episode_uuid ON entity_alias TYPE option<string>;
    DEFINE FIELD IF NOT EXISTS created_at          ON entity_alias TYPE datetime;
    DEFINE INDEX IF NOT EXISTS entity_alias_uuid_idx   ON entity_alias FIELDS uuid UNIQUE;
    DEFINE INDEX IF NOT EXISTS entity_alias_lookup     ON entity_alias FIELDS group_id, normalized_alias;
    DEFINE INDEX IF NOT EXISTS entity_alias_unique     ON entity_alias FIELDS group_id, normalized_alias UNIQUE;
    DEFINE INDEX IF NOT EXISTS entity_alias_entity_idx ON entity_alias FIELDS group_id, entity_uuid;

    -- Resource: a graph-visible card for content owned by another system.
    -- It deliberately is not an entity: its contents remain in the Library.
    DEFINE TABLE IF NOT EXISTS resource SCHEMAFULL;
    DEFINE FIELD IF NOT EXISTS uuid ON resource TYPE string;
    DEFINE FIELD IF NOT EXISTS group_id ON resource TYPE string;
    DEFINE FIELD IF NOT EXISTS library_item_id ON resource TYPE string;
    DEFINE FIELD IF NOT EXISTS title ON resource TYPE string;
    DEFINE FIELD IF NOT EXISTS kind ON resource TYPE string DEFAULT "document";
    DEFINE FIELD IF NOT EXISTS relationship ON resource TYPE string DEFAULT "reference_material";
    DEFINE FIELD IF NOT EXISTS summary ON resource TYPE string DEFAULT "";
    DEFINE FIELD IF NOT EXISTS topics ON resource TYPE array<string> DEFAULT [];
    DEFINE FIELD IF NOT EXISTS available ON resource TYPE bool DEFAULT true;
    DEFINE FIELD IF NOT EXISTS created_at ON resource TYPE datetime;
    DEFINE FIELD IF NOT EXISTS updated_at ON resource TYPE datetime;
    DEFINE INDEX IF NOT EXISTS resource_uuid_idx ON resource FIELDS uuid UNIQUE;
    DEFINE INDEX IF NOT EXISTS resource_library_item_idx ON resource FIELDS group_id, library_item_id UNIQUE;
    DEFINE INDEX IF NOT EXISTS resource_group_idx ON resource FIELDS group_id;

    -- Community ---------------------------------------------------------
    DEFINE TABLE IF NOT EXISTS community SCHEMAFULL;
    DEFINE FIELD IF NOT EXISTS uuid           ON community TYPE string;
    DEFINE FIELD IF NOT EXISTS group_id       ON community TYPE string;
    DEFINE FIELD IF NOT EXISTS name           ON community TYPE string;
    DEFINE FIELD IF NOT EXISTS summary        ON community TYPE string DEFAULT "";
    DEFINE FIELD IF NOT EXISTS name_embedding ON community TYPE option<array<float>>;
    DEFINE FIELD IF NOT EXISTS created_at     ON community TYPE datetime;
    -- Cognitive layer (additive). ` + "`" + `` + "`" + `kind` + "`" + `` + "`" + ` discriminates a normal
    -- entity-cluster ("cluster") from cognitive sidecars stored as
    -- community rows ("prediction"). ` + "`" + `` + "`" + `domain` + "`" + `` + "`" + ` carries the labelled
    -- semantic domain assigned by domain-aware clustering.
    -- ` + "`" + `` + "`" + `payload` + "`" + `` + "`" + ` is a free-form bag (e.g. prediction bundle).
    DEFINE FIELD IF NOT EXISTS kind           ON community TYPE string DEFAULT "cluster";
    DEFINE FIELD IF NOT EXISTS domain         ON community TYPE option<string>;
    DEFINE FIELD IF NOT EXISTS payload        ON community TYPE object FLEXIBLE DEFAULT {};
    DEFINE INDEX IF NOT EXISTS community_uuid_idx ON community FIELDS uuid UNIQUE;
    DEFINE INDEX IF NOT EXISTS community_kind_idx ON community FIELDS group_id, kind;

    -- Edges -------------------------------------------------------------
    DEFINE TABLE IF NOT EXISTS mentions SCHEMAFULL TYPE RELATION FROM episode TO entity;
    DEFINE FIELD IF NOT EXISTS uuid       ON mentions TYPE string;
    DEFINE FIELD IF NOT EXISTS group_id   ON mentions TYPE string;
    DEFINE FIELD IF NOT EXISTS created_at ON mentions TYPE datetime;
    DEFINE INDEX IF NOT EXISTS mentions_uuid_idx  ON mentions FIELDS uuid UNIQUE;
    DEFINE INDEX IF NOT EXISTS mentions_group_idx ON mentions FIELDS group_id;

    DEFINE TABLE IF NOT EXISTS relates_to SCHEMAFULL TYPE RELATION FROM entity TO entity;
    DEFINE FIELD IF NOT EXISTS uuid           ON relates_to TYPE string;
    DEFINE FIELD IF NOT EXISTS group_id       ON relates_to TYPE string;
    DEFINE FIELD IF NOT EXISTS name           ON relates_to TYPE string;
    DEFINE FIELD IF NOT EXISTS fact           ON relates_to TYPE string;
    DEFINE FIELD IF NOT EXISTS fact_embedding ON relates_to TYPE option<array<float>>;
    DEFINE FIELD IF NOT EXISTS episodes       ON relates_to TYPE array<string> DEFAULT [];
    DEFINE FIELD IF NOT EXISTS valid_at       ON relates_to TYPE option<datetime>;
    DEFINE FIELD IF NOT EXISTS invalid_at     ON relates_to TYPE option<datetime>;
    DEFINE FIELD IF NOT EXISTS expired_at     ON relates_to TYPE option<datetime>;
    DEFINE FIELD IF NOT EXISTS attributes     ON relates_to TYPE object FLEXIBLE DEFAULT {};
    DEFINE FIELD IF NOT EXISTS created_at     ON relates_to TYPE datetime;
    -- Generic temporal-state metadata: enables the singleton-slot closer
    -- and current-state queries without a hardcoded predicate vocabulary.
    DEFINE FIELD IF NOT EXISTS status         ON relates_to TYPE string DEFAULT "active";
    DEFINE FIELD IF NOT EXISTS polarity       ON relates_to TYPE string DEFAULT "positive";
    DEFINE FIELD IF NOT EXISTS source_type    ON relates_to TYPE string DEFAULT "user";
    DEFINE FIELD IF NOT EXISTS confidence     ON relates_to TYPE float DEFAULT 1.0;
    DEFINE FIELD IF NOT EXISTS temporal       ON relates_to TYPE bool DEFAULT false;
    DEFINE FIELD IF NOT EXISTS singleton      ON relates_to TYPE bool DEFAULT false;
    DEFINE FIELD IF NOT EXISTS domain         ON relates_to TYPE option<string>;
    DEFINE FIELD IF NOT EXISTS supersedes     ON relates_to TYPE array<string> DEFAULT [];
    DEFINE FIELD IF NOT EXISTS superseded_by  ON relates_to TYPE option<string>;
    -- Deterministic dedupe key (group_id::subject_uuid::predicate::object_uuid).
    -- Empty default keeps backward compatibility with rows written before
    -- this field existed; ` + "`" + `` + "`" + `backfill_fact_keys()` + "`" + `` + "`" + ` populates them so the
    -- unique index can be enabled after migration.
    DEFINE FIELD IF NOT EXISTS fact_key       ON relates_to TYPE string DEFAULT "";
    -- Relation-frame metadata (generalized predicate layer). Optional
    -- on legacy rows; populated on insert once a frame resolves.
    DEFINE FIELD IF NOT EXISTS relation_frame_id ON relates_to TYPE option<string>;
    DEFINE FIELD IF NOT EXISTS canonical_name    ON relates_to TYPE string DEFAULT "";
    DEFINE FIELD IF NOT EXISTS qualifiers        ON relates_to TYPE object FLEXIBLE DEFAULT {};
    DEFINE FIELD IF NOT EXISTS roles             ON relates_to TYPE object FLEXIBLE DEFAULT {};
    DEFINE FIELD IF NOT EXISTS conflict_group_id ON relates_to TYPE option<string>;
    DEFINE FIELD IF NOT EXISTS derived           ON relates_to TYPE bool DEFAULT false;
    DEFINE FIELD IF NOT EXISTS derived_from      ON relates_to TYPE option<string>;
    -- Cognitive layer (additive). All optional / safe defaults so legacy
    -- rows load forward without backfill. Populated lazily by
    -- ` + "`" + `` + "`" + `surriti.cognition` + "`" + `` + "`" + ` (reinforcement / decay / consolidation /
    -- belief / affect passes) and read by recall + rerankers.
    DEFINE FIELD OVERWRITE weight             ON relates_to TYPE option<float> DEFAULT 1.0;
    DEFINE FIELD OVERWRITE reinforcement_count ON relates_to TYPE option<int> DEFAULT 1;
    DEFINE FIELD IF NOT EXISTS last_reinforced_at  ON relates_to TYPE option<datetime>;
    DEFINE FIELD OVERWRITE recall_count        ON relates_to TYPE option<int> DEFAULT 0;
    DEFINE FIELD IF NOT EXISTS last_recalled_at    ON relates_to TYPE option<datetime>;
    DEFINE FIELD OVERWRITE decay_score          ON relates_to TYPE option<float> DEFAULT 1.0;
    DEFINE FIELD IF NOT EXISTS stability            ON relates_to TYPE string DEFAULT "episodic";
    DEFINE FIELD IF NOT EXISTS valence              ON relates_to TYPE option<float>;
    DEFINE FIELD IF NOT EXISTS intensity            ON relates_to TYPE option<float>;
    DEFINE FIELD IF NOT EXISTS consolidates         ON relates_to TYPE array<string> DEFAULT [];
    DEFINE FIELD IF NOT EXISTS is_belief            ON relates_to TYPE bool DEFAULT false;
    DEFINE FIELD IF NOT EXISTS belief_holder        ON relates_to TYPE option<string>;
    DEFINE INDEX IF NOT EXISTS relates_to_uuid_idx  ON relates_to FIELDS uuid UNIQUE;
    DEFINE INDEX IF NOT EXISTS relates_to_group_idx ON relates_to FIELDS group_id;
    DEFINE INDEX IF NOT EXISTS relates_to_active_idx ON relates_to FIELDS group_id, in, name, status;
    DEFINE INDEX IF NOT EXISTS relates_to_canonical_idx ON relates_to FIELDS group_id, in, canonical_name, status;
    DEFINE INDEX IF NOT EXISTS relates_to_conflict_idx ON relates_to FIELDS group_id, conflict_group_id;
    DEFINE INDEX IF NOT EXISTS relates_to_fact_key_idx ON relates_to FIELDS group_id, fact_key;
    DEFINE INDEX IF NOT EXISTS relates_to_fact_fts  ON relates_to FIELDS fact
        FULLTEXT ANALYZER surriti_en BM25 HIGHLIGHTS;
    DEFINE INDEX IF NOT EXISTS relates_to_fact_hnsw ON relates_to FIELDS fact_embedding
        HNSW DIMENSION {{DIM}} DIST COSINE TYPE F32;

    DEFINE TABLE IF NOT EXISTS memory_ref SCHEMAFULL TYPE RELATION FROM entity TO relates_to;
    DEFINE FIELD IF NOT EXISTS uuid              ON memory_ref TYPE string;
    DEFINE FIELD IF NOT EXISTS group_id          ON memory_ref TYPE string;
    DEFINE FIELD IF NOT EXISTS role              ON memory_ref TYPE string;
    -- Stable principal, deliberately separate from the source graph's entity id.
    DEFINE FIELD IF NOT EXISTS viewer_id         ON memory_ref TYPE option<string>;
    DEFINE FIELD IF NOT EXISTS fact_uuid         ON memory_ref TYPE option<string>;
    DEFINE FIELD IF NOT EXISTS source_actor_uuid ON memory_ref TYPE option<string>;
    DEFINE FIELD IF NOT EXISTS episode_uuid      ON memory_ref TYPE option<string>;
    DEFINE FIELD IF NOT EXISTS conversation_id   ON memory_ref TYPE option<string>;
    DEFINE FIELD IF NOT EXISTS valid_at          ON memory_ref TYPE datetime;
    DEFINE FIELD IF NOT EXISTS invalid_at        ON memory_ref TYPE option<datetime>;
    DEFINE FIELD IF NOT EXISTS created_at        ON memory_ref TYPE datetime;
    DEFINE INDEX IF NOT EXISTS memory_ref_uuid_idx ON memory_ref FIELDS uuid UNIQUE;
    DEFINE INDEX IF NOT EXISTS memory_ref_user_idx ON memory_ref FIELDS group_id, in, role, invalid_at;
    DEFINE INDEX IF NOT EXISTS memory_ref_viewer_active_idx ON memory_ref FIELDS viewer_id, invalid_at, out;
    DEFINE INDEX IF NOT EXISTS memory_ref_viewer_fact_active_idx ON memory_ref FIELDS viewer_id, fact_uuid, invalid_at;
    DEFINE INDEX IF NOT EXISTS memory_ref_fact_idx ON memory_ref FIELDS group_id, out, role, invalid_at;

    -- Relation frames (per-predicate metadata that drives generalized
    -- temporal/contradiction reasoning without hardcoded predicate
    -- vocabulary). One row per canonical relation type per group.
    DEFINE TABLE IF NOT EXISTS relation_frame SCHEMAFULL;
    DEFINE FIELD IF NOT EXISTS uuid                 ON relation_frame TYPE string;
    DEFINE FIELD IF NOT EXISTS group_id             ON relation_frame TYPE string DEFAULT "";
    DEFINE FIELD IF NOT EXISTS canonical_name       ON relation_frame TYPE string;
    DEFINE FIELD IF NOT EXISTS aliases              ON relation_frame TYPE array<string> DEFAULT [];
    DEFINE FIELD IF NOT EXISTS description          ON relation_frame TYPE string DEFAULT "";
    DEFINE FIELD IF NOT EXISTS directionality       ON relation_frame TYPE string DEFAULT "unknown";
    DEFINE FIELD IF NOT EXISTS temporal_kind        ON relation_frame TYPE string DEFAULT "unknown";
    DEFINE FIELD IF NOT EXISTS cardinality          ON relation_frame TYPE string DEFAULT "unknown";
    DEFINE FIELD IF NOT EXISTS contradiction_policy ON relation_frame TYPE string DEFAULT "uncertain";
    DEFINE FIELD IF NOT EXISTS inverse_name         ON relation_frame TYPE option<string>;
    DEFINE FIELD IF NOT EXISTS subject_role         ON relation_frame TYPE option<string>;
    DEFINE FIELD IF NOT EXISTS object_role          ON relation_frame TYPE option<string>;
    DEFINE FIELD IF NOT EXISTS confidence           ON relation_frame TYPE float DEFAULT 0.5;
    DEFINE FIELD IF NOT EXISTS created_at           ON relation_frame TYPE datetime;
    DEFINE INDEX IF NOT EXISTS relation_frame_uuid_idx ON relation_frame FIELDS uuid UNIQUE;
    DEFINE INDEX IF NOT EXISTS relation_frame_canon_idx ON relation_frame FIELDS group_id, canonical_name UNIQUE;

    DEFINE TABLE IF NOT EXISTS has_member SCHEMAFULL TYPE RELATION FROM community TO entity;
    DEFINE FIELD IF NOT EXISTS uuid       ON has_member TYPE string;
    DEFINE FIELD IF NOT EXISTS group_id   ON has_member TYPE string;
    DEFINE FIELD IF NOT EXISTS created_at ON has_member TYPE datetime;
    DEFINE INDEX IF NOT EXISTS has_member_uuid_idx ON has_member FIELDS uuid UNIQUE;
    `
const schemaCompatibilityPreflight = "\nDEFINE TABLE IF NOT EXISTS episode SCHEMAFULL;\nDEFINE TABLE IF NOT EXISTS entity SCHEMAFULL;\nDEFINE TABLE IF NOT EXISTS community SCHEMAFULL;\nDEFINE TABLE IF NOT EXISTS relates_to SCHEMAFULL TYPE RELATION FROM entity TO entity;\nDEFINE FIELD OVERWRITE affect ON episode TYPE option<object> FLEXIBLE;\nDEFINE FIELD OVERWRITE traits ON entity TYPE option<array<string>>;\nDEFINE FIELD OVERWRITE goals_active ON entity TYPE option<array<string>>;\nDEFINE FIELD OVERWRITE kind ON community TYPE option<string>;\nDEFINE FIELD OVERWRITE payload ON community TYPE option<object> FLEXIBLE;\nDEFINE FIELD OVERWRITE stability ON relates_to TYPE option<string>;\nDEFINE FIELD OVERWRITE consolidates ON relates_to TYPE option<array<string>>;\nDEFINE FIELD OVERWRITE is_belief ON relates_to TYPE option<bool>;\n"
const schemaCompatibilityStrict = "\nDEFINE FIELD OVERWRITE affect ON episode TYPE object FLEXIBLE DEFAULT {};\nDEFINE FIELD OVERWRITE traits ON entity TYPE array<string> DEFAULT [];\nDEFINE FIELD OVERWRITE goals_active ON entity TYPE array<string> DEFAULT [];\nDEFINE FIELD OVERWRITE kind ON community TYPE string DEFAULT \"cluster\";\nDEFINE FIELD OVERWRITE payload ON community TYPE object FLEXIBLE DEFAULT {};\nDEFINE FIELD OVERWRITE stability ON relates_to TYPE string DEFAULT \"episodic\";\nDEFINE FIELD OVERWRITE consolidates ON relates_to TYPE array<string> DEFAULT [];\nDEFINE FIELD OVERWRITE is_belief ON relates_to TYPE bool DEFAULT false;\n"

var schemaCompatibilityBackfills = []string{
	"UPDATE memory_ref SET viewer_id =\n    (SELECT VALUE name FROM entity WHERE id = $parent.in LIMIT 1)[0]\n   WHERE viewer_id IS NONE OR viewer_id = \"\";",
	"UPDATE memory_ref SET fact_uuid =\n    (SELECT VALUE uuid FROM relates_to WHERE id = $parent.out LIMIT 1)[0]\n   WHERE fact_uuid IS NONE OR fact_uuid = \"\";",
	"UPDATE episode SET affect = {} WHERE affect IS NONE;",
	"UPDATE entity SET traits = [], goals_active = [] WHERE traits IS NONE OR goals_active IS NONE;",
	"UPDATE community SET kind = \"cluster\", payload = {} WHERE kind IS NONE OR payload IS NONE;",
	"UPDATE relates_to SET recall_count = 0 WHERE recall_count IS NONE;",
	"UPDATE relates_to SET reinforcement_count = 1 WHERE reinforcement_count IS NONE;",
	"UPDATE relates_to SET weight = 1.0 WHERE weight IS NONE;",
	"UPDATE relates_to SET decay_score = 1.0 WHERE decay_score IS NONE;",
	"UPDATE relates_to SET stability = \"episodic\", consolidates = [], is_belief = false\n WHERE stability IS NONE OR consolidates IS NONE OR is_belief IS NONE;",
	"UPDATE relates_to SET\n    status = IF status = \"silent\" THEN \"active\" ELSE status END,\n    attributes = object::extend(attributes, {\n        activation_events: [[time::now(), 1.0]],\n        activation_total_weight: 1.0,\n        activation_created_at: created_at\n    })\nWHERE status IN [\"active\", \"silent\"]\n  AND attributes.activation_events IS NONE;",
	"UPDATE entity SET salience = 0.0 WHERE salience IS NONE;",
	"UPDATE entity SET mention_count = 0 WHERE mention_count IS NONE;",
}

func SchemaDDL(embeddingDim int) (string, error) {
	if embeddingDim <= 0 {
		return "", fmt.Errorf("%w: embedding_dim must be positive, got %d", ErrConfig, embeddingDim)
	}
	return strings.ReplaceAll(schemaDDLTemplate, "{{DIM}}", fmt.Sprint(embeddingDim)), nil
}

func BackfillFactKeys(ctx context.Context, driver Queryer) (int, error) {
	result, err := driver.Query(ctx, "\nSELECT uuid, group_id, name, qualifiers,\n    record::id(in)  AS subject_uuid,\n    record::id(out) AS object_uuid\nFROM relates_to WHERE fact_key = \"\" OR fact_key IS NONE;\n", nil)
	if err != nil {
		return 0, err
	}
	rows := UnwrapRows(result)
	updated := 0
	for _, row := range rows {
		key := MakeFactKey(
			stringFromAny(row["group_id"]),
			stringFromAny(row["subject_uuid"]),
			stringFromAny(row["name"]),
			stringFromAny(row["object_uuid"]),
			QualifierHash(mapFromAny(row["qualifiers"])),
		)
		if _, err := driver.Query(ctx,
			"UPDATE relates_to SET fact_key = $key WHERE uuid = $uuid",
			map[string]any{"key": key, "uuid": row["uuid"]},
		); err != nil {
			return updated, err
		}
		updated++
	}
	return updated, nil
}

func EnforceFactKeyUniqueness(ctx context.Context, driver Queryer) error {
	if _, err := BackfillFactKeys(ctx, driver); err != nil {
		return err
	}
	result, err := driver.Query(ctx, "\nSELECT group_id, fact_key, count() AS count\nFROM relates_to\nWHERE fact_key != \"\" AND invalid_at IS NONE\nGROUP BY group_id, fact_key;\n", nil)
	if err != nil {
		return err
	}
	var collisions []map[string]any
	for _, row := range UnwrapRows(result) {
		if intFromAny(row["count"]) > 1 {
			collisions = append(collisions, row)
		}
	}
	if len(collisions) > 0 {
		sample := collisions[0]
		return fmt.Errorf(
			"cannot enforce relates_to fact_key uniqueness: found %d collision(s); for example group_id=%q, fact_key=%q; reconcile duplicate edges and retry schema initialization",
			len(collisions), stringFromAny(sample["group_id"]), stringFromAny(sample["fact_key"]),
		)
	}
	_, err = driver.Query(ctx, `
DEFINE FIELD OVERWRITE fact_key_version ON relates_to TYPE string
VALUE IF invalid_at IS NONE THEN fact_key ELSE string::concat(fact_key, "::history::", uuid) END;
UPDATE relates_to SET fact_key = fact_key WHERE fact_key_version IS NONE;
DEFINE INDEX OVERWRITE relates_to_fact_key_idx
ON relates_to FIELDS group_id, fact_key_version UNIQUE;
`, nil)
	return err
}

// InitSchema applies the Python compatibility-first migration sequence. It is
// idempotent, preserves provenance, and never publishes a stricter schema until
// legacy rows have been backfilled and duplicate fact keys quarantined.
func (d *SurrealDriver) InitSchema(ctx context.Context) error {
	if _, err := d.Query(ctx, schemaCompatibilityPreflight, nil); err != nil {
		return schemaInitError(ctx, err)
	}
	ddl, err := SchemaDDL(d.cfg.EmbeddingDim)
	if err != nil {
		return schemaInitError(ctx, err)
	}
	if _, err := d.Query(ctx, ddl, nil); err != nil {
		return schemaInitError(ctx, err)
	}
	for _, q := range schemaCompatibilityBackfills {
		if _, err := d.Query(ctx, q, nil); err != nil {
			return schemaInitError(ctx, err)
		}
	}
	if _, err := BackfillFactKeys(ctx, d); err != nil {
		return schemaInitError(ctx, err)
	}
	if _, _, err := ReconcileFactKeyCollisions(ctx, d, ReconcileMergeByFactKey); err != nil {
		return schemaInitError(ctx, err)
	}
	if err := EnforceFactKeyUniqueness(ctx, d); err != nil {
		return schemaInitError(ctx, err)
	}
	if _, err := d.Query(ctx, schemaCompatibilityStrict, nil); err != nil {
		return schemaInitError(ctx, err)
	}
	return nil
}

func schemaInitError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return fmt.Errorf("%w: failed to initialise schema: %v", ErrSchema, err)
}
