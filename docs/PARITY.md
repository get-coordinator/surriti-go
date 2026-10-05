# Surriti Python -> Go parity map

Reference: `the-hack-foundation/surriti@3e4a26d8e624f60bc3e58581a9336e5fb569aab4`

This document is the implementation contract for the parity phase. "Ported" means behaviorally equivalent, not merely represented by a similarly named type or function.

## Non-negotiable invariants

- SurrealDB is the source of truth.
- Existing SurrealDB schema and stored rows remain compatible.
- Temporal history is preserved; corrections supersede/invalidate rather than erase history.
- Fact identity and deduplication remain deterministic.
- Tenant/group isolation is mandatory.
- Participant memory visibility is enforced before unrestricted candidates are returned.
- Retry/reconnect logic must be concurrency-safe.
- Ingest must remain resilient to cognition/profile background failures.
- Provider implementations are injected behind interfaces. Core Surriti has no OpenRouter or Coordinator dependency.
- Memory-pack import/export must remain deterministic, safe and idempotent.
- Any behavior intentionally changed after parity requires a separately reviewed post-port change.

## Source inventory

The Python package contains 47 production Python modules and a large test corpus. The current test inventory contains 421 conventionally named tests across the principal test modules, plus stress/debug scripts.

### Public facade and runtime

| Python | Go target | Required behavior |
|---|---|---|
| `__init__.py` | `surriti.go`, public types/interfaces | Preserve the public capability surface and version metadata. |
| `graphiti.py` | `surriti.go`, `ingest.go`, `memory_refs.go`, `current_state.go`, `communities.go`, `self_model.go` | Main facade/orchestration. Do not port as one giant file; preserve every observable method and invariant. |
| `driver.py` | `driver.go` | Connection lifecycle, env config, context cancellation, stale-connection recovery, transaction-conflict retry, concurrency-safe reconnect, schema init and clear. |
| `errors.py` | `errors.go` | Stable typed/sentinel error categories with wrapping. |
| `_logging.py` | `logging.go` | Opt-in logging surface; no duplicate default handlers. |

### Persistent model

| Python | Go target | Required behavior |
|---|---|---|
| `nodes.py` | `models_nodes.go` | Episode, entity, alias, community models; defaults; UTC timestamps; episode source enum. |
| `edges.py` | `models_edges.go` | Mentions, entity facts, community membership; temporal state; cognition fields; relation-frame metadata; participant reference metadata. |
| `schema.py` | `schema.go` | Exact managed tables/fields/index semantics, compatibility backfills, fact-key uniqueness enforcement. |
| `utils.py` | `decode.go` | Surreal row -> model coercion, record-id normalization, datetime coercion. |
| `validators.py` | `validators.go` | Fact repair, identity self-loop repair, filler rejection, self-loop rules. |
| `resources.py` | `resources.go` | External Library resource cards, group scoping, upsert/list/availability. |
| `read_models.py` | `read_models.go` | Graph/resource/self/cognition read projections without changing source-of-truth semantics. |

Managed SurrealDB tables:

1. `episode`
2. `entity`
3. `entity_alias`
4. `resource`
5. `community`
6. `mentions`
7. `relates_to`
8. `memory_ref`
9. `relation_frame`
10. `has_member`

### LLM, embeddings and ranking boundaries

| Python | Go target | Required behavior |
|---|---|---|
| `llm.py` | `llm.go` | Provider-neutral LLM interface plus extracted entity/fact/contradiction structures and scripted/dummy test implementations. |
| `llm_clients.py` | adapters outside core or optional `adapters/` | Preserve prompt/output parsing semantics where adapters are supplied. Core library must not depend on a specific routing provider. |
| `embedder.py` | `embedder.go` | Provider-neutral embedder interface, deterministic dummy embedder, batch behavior and cosine similarity. |
| `rerankers.py` | `rerank.go` | Cross-encoder interface, dummy ranker, RRF, MMR, episode-mentions reranking. |

### Entity identity and profiles

| Python | Go target | Required behavior |
|---|---|---|
| `entity_resolution.py` | `entity_resolution.go` | Alias normalization, alias exact-hit fast path, semantic candidate resolution, optional LLM arbitration, missing-entity creation, alias persistence. |
| `profiles.py` | `profiles.go` | Touched-only entity dossier refresh, deterministic fallback summary, optional LLM summary, embedding refresh and backfill. |

### Relation semantics and temporal truth

| Python | Go target | Required behavior |
|---|---|---|
| `relation_frames.py` | `relation_frames.go` | Registry, defaults, tenant overrides, aliases, dynamic classification, directionality, temporal kind, cardinality, contradiction policy, qualifier hash, slot key, symmetric normalization. |
| `temporal.py` | `temporal.go` | Similar-edge candidate retrieval, structured contradiction evaluation, temporal invalidation/supersession. |
| `repair.py` | `repair.go` | Read-only collision inspection and deterministic fact-key reconciliation while preserving provenance. |

Core temporal behaviors that must be identical:

- `assert`
- `terminate`
- `correct`
- `noop`
- singleton slot closing
- assistant/tool/system source protection from user-truth replacement
- explicit `replaces`
- fact-key deduplication
- supersedes/superseded-by lineage
- valid-at / invalid-at queries
- current-state and as-of state
- relation-frame cardinality and coexist/conflict behavior

### Search and recall

| Python | Go target | Required behavior |
|---|---|---|
| `search.py` | `search.go` | Vector, BM25/full-text and hybrid retrieval, RRF fusion, filtering, validity handling, focal-distance reranking, node/episode/community search. |
| `search_filters.py` | `search_filters.go` | Date/property filters, OR-of-AND date semantics, null/property comparison behavior. |
| `search_recipes.py` | `search_recipes.go` | Immutable recipe constructors/constants for edge/node/community/combined modes. |
| `memory_retrieval.py` | `memory_retrieval.go` | Cue tokenization, absolute admission gate, spreading activation, source evidence snippets, silent-memory resurrection. |

Recall must preserve the rule: ranking may choose among relevant memories; ranking must not manufacture relevance.

### Participant/reference memory

Current `main` behavior is newer than the 0.5.0 README and is part of the parity target.

Port all of:

- stable `viewer_id`
- `fact_uuid` reference
- roles: `asserted`, `witnessed`, `granted`, `endorsed`, `disputed`
- participant-scoped recall
- authorization restriction in the candidate query
- participant forget without deleting canonical fact
- grant authorization
- grant/revoke
- reference role/metadata returned on facts
- legacy viewer/fact reference backfills

### Memory packs

| Python | Go target | Required behavior |
|---|---|---|
| `memory_pack.py` | `memory_pack.go` | Directory/ZIP export, JSONL, manifest/checksums, validation, zip safety, merge import, deterministic import IDs, origin stamping, temporal-history and relation-frame preservation. |

Portable graph tables remain:

- `entity`
- `entity_alias`
- `relation_frame`
- `relates_to`

Raw episodes, communities and mentions are intentionally not part of the default portable graph contract unless the Python behavior changes.

### Cognition layer

The cognition layer is in scope for parity. It remains additive and must never make base ingest fragile.

| Python | Go target | Behavior |
|---|---|---|
| `cognition/config.py` | `cognition_config.go` | Tunables/defaults. |
| `cognition/state.py` | `cognition_state.go` | Per-group dirty queue/state. |
| `cognition/scheduler.py` | `cognition_scheduler.go` | Debounce, recovery, per-group serialization, cross-group concurrency, shutdown. |
| `cognition/runner.py` | `cognition_runner.go` | Ordered fail-soft pass orchestration and metrics. |
| `cognition/affect.py` | `cognition_affect.go` | Deterministic affect scoring/tagging. |
| `cognition/perspective.py` | `cognition_perspective.go` | Belief detection/promotion. |
| `cognition/reinforcement.py` | `cognition_reinforcement.go` | Assertion and recall reinforcement metadata. |
| `cognition/decay.py` | `cognition_decay.go` | Half-life, linear vitality, ACT-R-like activation history, effective confidence, protected memory classes. |
| `cognition/associative.py` | `cognition_associative.go` | Weight refresh from current memory physiology. |
| `cognition/lifecycle.py` | `cognition_lifecycle.go` | Silence inactive memories without deleting them. |
| `cognition/procedural.py` | `cognition_procedural.go` | Interaction-pattern classification and synthetic procedural memories. |
| `cognition/traits.py` | `cognition_traits.go` | Candidate selection, optional ratification, persistence/cache. |
| `cognition/goals.py` | `cognition_goals.go` | Goal extraction/ratification/persistence. |
| `cognition/clustering.py` | `cognition_clustering.go` | Domain labeling of communities. |
| `cognition/consolidation.py` | `cognition_consolidation.go` | Repeated-fact and stagnant-edge abstraction with provenance. |
| `cognition/prediction.py` | `cognition_prediction.go` | Per-group prediction bundle. |
| `cognition/self_awareness.py` | `cognition_self_awareness.go` | Self episodes, traits, beliefs, patterns and self-model reads. |
| `cognition/_writes.py` | `cognition_writes.go` | Stable synthetic entity/edge writes. |
| `cognition/_jsonio.py` | `jsonutil.go` | Loose JSON extraction and snake-case normalization. |
| `cognition/prompts.py` | `prompts_cognition.go` | Preserve prompt contracts for injected LLM implementations. |

## Facade method parity

The Go `Surriti` facade must cover the current Python facade's observable operations:

- lifecycle: connect, close, schema/index initialization
- resources: upsert/list/set availability
- ingest: add episode, add bulk episodes, add triplet
- removal: episode, group, node, edge
- community building
- search and advanced search
- recall
- diagnostics: inspect, explain
- retrieve episodes
- get node/edge/episode
- save node/edge
- memory pack export/import
- user upsert
- participant forget/filter/recall/grant/revoke/authorization
- relation-frame register/get/merge/conflicts
- current profile
- current fact(s)
- facts/state as of a timestamp
- self episode and self model

The private helper structure need not match Python, but all data-safety behavior in those helpers must.

## Reliability requirements

### Connection lifecycle

Equivalent to Python `driver.py`:

- connect is idempotent
- concurrent connect calls create one effective client
- partial/cancelled connections are closed
- operations accept `context.Context`
- stale connection detection is bounded and typed
- concurrent stale queries coordinate one reconnect
- transaction conflicts retry without throwing away a healthy connection
- retry count is bounded
- exponential backoff includes jitter
- close is idempotent
- replacement logic never closes a newly established healthy connection due to an older failed request

### Write safety

- deterministic UUID/fact keys where Python uses deterministic identity
- unique-key races are handled as expected concurrent outcomes, not corruption
- duplicate extraction is deduplicated before write
- retries do not duplicate facts, references or provenance
- group scoping is present on every tenant-sensitive query
- canonical facts are not deleted when a participant forgets them
- schema migration never silently drops conflicting rows

### Background cognition/profile work

- base ingest completion does not depend on cognition success
- failures are logged/observable
- failed episodes are retryable
- shutdown waits/cancels safely before DB teardown
- same group does not run overlapping cognition passes
- different groups may run concurrently
- restart recovers persisted-but-unprocessed episodes

## Test-port matrix

The Python suite currently exercises at least these groups:

- activation lifecycle: 6 tests
- cognition E2E: 12
- cognition unit/scheduler: 33
- comprehensive integration: 38
- cycle integration: 16
- diagnostics: 10
- driver reliability: 7
- entity resolution: 4
- family integration: 37
- general integration: 57
- live SurrealDB integration: 34
- memory packs: 23
- memory retrieval: 11
- models: 7
- fake-driver pipeline: 37
- scripted prompts: 7
- read models/resources: 2
- recall/profiles: 4
- relation frames: 22
- repair: 4
- resources: 2
- restart preservation: 2
- schema: 1
- SDK surface: 14
- unit extras/search/ranking: 31

Total conventional tests inventoried: **421**.

Do not translate assertions blindly. Each Go test should document the invariant it protects.

## Implementation order

### Gate 0 - freeze the reference

- Record Python baseline commit.
- No new Python behavior is silently folded into the Go port.
- If Python main advances, explicitly rebase the parity document and rerun differential fixtures.

### Gate 1 - foundations

- public models
- errors
- provider interfaces
- deterministic dummy/scripted providers
- pure utilities
- relation-frame pure helpers
- search filters
- cognition decay pure functions

No database required.

### Gate 2 - Surreal driver and exact schema

- official SurrealDB Go SDK adapter
- connection/reconnect/retry state machine
- schema DDL
- compatibility backfills
- fact-key collision repair
- integration harness against pinned SurrealDB 3.x

This gate must be green before graph writes.

### Gate 3 - canonical graph writes

- episode/entity/alias persistence
- entity resolution
- mentions
- fact insert/dedupe
- temporal operations
- relation frames
- user identity
- participant references

### Gate 4 - retrieval

- vector/full-text search
- RRF/MMR/cross encoder
- filters
- admission
- spreading activation
- evidence snippets
- resurrection
- current/as-of reads
- participant-scoped reads

### Gate 5 - secondary features

- profiles
- resources
- communities
- read models
- diagnostics
- memory packs

### Gate 6 - cognition

Port the scheduler/runner first, then each pass independently. Preserve fail-soft semantics.

### Gate 7 - differential certification

Run the same deterministic fixture corpus through Python and Go against isolated databases, normalize nondeterministic timestamps/UUIDs where necessary, and compare:

- entities
- aliases
- episodes
- facts and temporal lineage
- memory references
- relation frames
- search/recall ordering where deterministic
- current/as-of state
- memory-pack output
- cognition outputs for deterministic/scripted LLM fixtures

## Definition of parity complete

Parity is complete only when:

- every item in this document is implemented or explicitly documented as intentionally external
- the Go suite reproduces the Python invariants
- all live SurrealDB integration tests pass under repeated runs
- `go test -race ./...` passes
- fuzz tests exist for parsers, fact repair, pack validation and malformed Surreal row decoding
- cancellation/restart tests pass
- differential fixtures pass
- Python and Go can read/write the same supported database schema without migration forks
- no vendor/provider dependency leaks into the core interfaces
