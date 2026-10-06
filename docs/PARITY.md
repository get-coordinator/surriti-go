# Surriti Python -> Go parity map

Original reference: `the-hack-foundation/surriti@3e4a26d8e624f60bc3e58581a9336e5fb569aab4`. The 2026-10-06 acceptance audit also fixes confirmed defects in the working Python reference, as explicitly requested by the user.

This document is the implementation contract for the parity phase. "Ported" means behaviorally equivalent, not merely represented by a similarly named type or function.

## Port status

The source audit maps every production module and public facade capability to a Go implementation below. Both working libraries share the schema migrations, compatibility backfills, temporal operations, memory references, retrieval pipeline, and cognition pass order. This inventory is a source coverage statement; it is not a claim that every possible input has been differentially certified.

Python's `testing.py`, `py.typed`, packaging, and Python-only development scripts are excluded. Go uses native test doubles and the production SurrealDB SDK.

## Corrections from the final audit

- Corrected the SDK datetime boundary: plain CBOR timestamps lost fractional seconds and returned SDK wrapper types that core decoding did not recognize. Queries now preserve timestamp precision and recursively normalize returned datetimes/NONE.
- Preserved integer/float distinctions and large JSON integers for qualifier hashing, extraction, and pack imports. Python float notation, ASCII escaping, Unicode case folding, and contradiction index parsing now match the reference contracts.
- Restored exact extraction/contradiction prompts; kept cognition prompt constants byte-equivalent. Preserved first-seen ordering for tied search results, affect labels, trait candidates, goal speakers, self-model patterns, and graph read projections.
- Unified basic search with its compatibility path. Explicit zero search tuning is retained. Restricted silent-memory resurrection before candidate retrieval when participant authorization is present.
- Corrected scheduler debounce/retry behavior, shutdown ownership of manual passes, context-aware group serialization, and restart recovery. Self episodes now notify cognition. Failed steps log their underlying errors.
- Prevented a stale request from reopening a driver after `Close`; preserved explicit reconnect and partial-connection cleanup.
- Preserved structured row arrays in read models, profile summary Unicode boundaries, and trait evidence-index semantics.
- Fixed vector export, JSONL flush/close error handling, missing ZIP checksum targets, unsafe archive paths, and colliding export temporary files. Imports retain deterministic IDs and the baseline merge model.
- Consolidated adapter HTTP handling with bounded transient retries, cancellation, checked request encoding/response reads, response size limits, and optional fail-soft synthesis.
- Corrected the module path; grouped participant operations, state reads, options, and prompt contracts into cohesive files. Removed duplicate basic-search and scheduler execution paths and redundant min/max helpers. Applied standard Go formatting throughout the formerly compressed source.

## Executed validation

- `go fmt ./...`, `go vet ./...`, `go test ./...`, and `go test -race ./...`.
- Isolated in-memory SurrealDB **3.0.5**, using unique disposable databases: schema initialization, singleton replacement, repeated episode ingestion, timestamp precision, participant recall/isolation/grant/revoke/forget, self-episode scheduling, a default cognition pass and processing markers, pack export/import and repeated merge with embeddings.
- Python writes a timestamped fact; Go reads it, resolves its Unicode-equivalent subject and replaces its singleton state; Python verifies current and historical results. Python exports a pack which Go imports and exports; Python validates/imports that Go pack and checks active/history counts.
- Frozen Python golden fixtures for schema DDL, extraction/classification/contradiction prompts, and qualifier hashes. All four cognition prompt constants were also compared byte-for-byte against Python.
- Targeted lifecycle/reconnect, scheduler shutdown/retry, parser, archive safety, ordering, read-model shape, and HTTP retry/cancellation tests.

No application/production database was modified for validation. The external sibling harness now exercises real OpenRouter extraction and 768-dimensional embeddings, differential public API scenarios, multi-client writes, and actual disk-backed server restarts. See `../../surriti-smoke/VALIDATION.md` for current evidence. Production-volume and extended outage/soak behavior still require workload-specific validation.

## Preserved baseline behavior and safety boundaries

Equivalent-edge lookup now respects qualifiers and singleton validity intervals. Pack imports retain qualifier identity and distinct historical versions. The unique fact-key index uses a computed version key: current facts retain their semantic key, while invalidated versions include their UUID. Existing current-key collisions still require explicit repair; valid history is never treated as a duplicate to delete. Python's placeholder self-model goals result remains empty where the reference does so.

Go-specific safety mechanisms include context-aware synchronization, refusing unsafe ZIP members, bounded HTTP response reads, unique atomic-export temporary files, and applying participant authorization to silent-memory resurrection. These do not add managed tables. The shared additive fact-key version field preserves the original semantic fact key while allowing historical recurrence. Background shutdown assumes injected capabilities honor their contexts. Provider clients remain application-owned, as in the Python facade.

The adapter retry defaults follow the upstream [OpenAI Python SDK](https://github.com/openai/openai-python#retries) and [Anthropic Python SDK](https://github.com/anthropics/anthropic-sdk-python#retries); transport timing need not be byte-identical across languages. The core remains provider-neutral.

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
- Shared behavior changes must be explicit, implemented in both runtimes, and covered by differential and independent invariant checks.

## Acceptance audit changes (2026-10-06)

- Persist relation-frame identity before facts reference it; reload stored frames on connection, including used merged aliases.
- Keep numeric values distinct during semantic entity resolution; retain resolved surface-name mappings and recover omitted extraction endpoints.
- Record declared name/alias relationships and honor explicit alias bindings over literal alias-value nodes.
- Include explicit start/end timestamps in the extraction contract; both production prompts and golden fixtures were updated together.
- Make replacement, conflict marking, and supersession writes atomic. Serialize competing singleton writes through a subject write inside the transaction and re-evaluate the slot on retry. Insert late historical assertions into the correct validity interval while retaining newer state; scheduled future changes leave current facts valid until their boundary.
- Preserve historical versions and qualifiers through deduplication, schema migration, collision inspection, and repeated pack imports.
- Include every competing fact in unresolved conflict groups; preserve empty-group fallback behavior in Go bulk ingestion.
- Fix raw read-model parsing, diagnostic SurrealQL, and vector result ordering. Scoped retrieval uses exact cosine ranking to avoid global ANN omissions across tenants and authorized subsets.
- Check all Python query-statement errors and preserve the underlying transaction failure for retries. Recognize SDK receive-task shutdown without swallowing caller cancellation.
- Prevent Go connection-failure cleanup panics. Observe WebSocket close frames and heartbeat failures so pending/idle requests unblock and the existing driver reconnects after a real server restart.

Registry registration remains synchronous: a frame configured after connection is persisted when used by ingestion. Do not interpret an unused in-memory registration as an acknowledged database write.

## Source inventory

The Python package contains the runtime modules inventoried below, plus Python-only packaging/test support. Native tests and the external acceptance harness both contribute evidence; current executed counts are recorded in the harness validation report.

### Public facade and runtime

| Python | Go target | Required behavior |
|---|---|---|
| `__init__.py` | `surriti.go`, `options.go`, public types/interfaces | Preserve the public capability surface and version metadata. |
| `graphiti.py` | `surriti.go`, `ingest.go`, `graph_write.go`, `graph_api.go`, `graph_state.go`, `participant_memory.go`, `recall.go`, `communities.go`, `diagnostics.go`, `self_awareness.go`, `facade_compat.go` | Main facade/orchestration. Do not port as one giant file; preserve every observable method and invariant. |
| `driver.py` | `driver.go`, `internal/surrealtransport` | Connection lifecycle, env config, context cancellation, stale-connection recovery, transaction-conflict retry, concurrency-safe reconnect, schema init and clear. |
| `errors.py` | `errors.go` | Stable typed/sentinel error categories with wrapping. |
| `_logging.py` | `logging.go` | Opt-in logging surface; no duplicate default handlers. |

### Persistent model

| Python | Go target | Required behavior |
|---|---|---|
| `nodes.py` | `models.go` | Episode, entity, alias, community models; defaults; UTC timestamps; episode source enum. |
| `edges.py` | `models.go` | Mentions, entity facts, community membership; temporal state; cognition fields; relation-frame metadata; participant reference metadata. |
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
| `llm_clients.py` | `llm_clients.go`, `llm_parse.go`, `llm_prompts.go`, `provider_*.go`, `internal/providerhttp` | Preserve prompt/output parsing semantics where adapters are supplied. Core library must not depend on a specific routing provider. |
| `embedder.py` | `embedder.go`, `provider_openai_embedding.go` | Provider-neutral embedder interface, deterministic dummy embedder, batch behavior and cosine similarity. |
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
- `qualify`
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
| `cognition/state.py` | `cognition_scheduler.go` | Per-group dirty queue/state. |
| `cognition/scheduler.py` | `cognition_scheduler.go` | Debounce, recovery, per-group serialization, cross-group concurrency, shutdown. |
| `cognition/runner.py` | `cognition_runner.go` | Ordered fail-soft pass orchestration and metrics. |
| `cognition/affect.py` | `cognition_core_passes.go` | Deterministic affect scoring/tagging. |
| `cognition/perspective.py` | `cognition_core_passes.go` | Belief detection/promotion. |
| `cognition/reinforcement.py` | `cognition_reinforcement.go` | Assertion and recall reinforcement metadata. |
| `cognition/decay.py` | `cognition_decay.go` | Half-life, linear vitality, ACT-R-like activation history, effective confidence, protected memory classes. |
| `cognition/associative.py` | `cognition_core_passes.go` | Weight refresh from current memory physiology. |
| `cognition/lifecycle.py` | `cognition_core_passes.go` | Silence inactive memories without deleting them. |
| `cognition/procedural.py` | `cognition_context_passes.go` | Interaction-pattern classification and synthetic procedural memories. |
| `cognition/traits.py` | `cognition_traits_goals.go` | Candidate selection, optional ratification, persistence/cache. |
| `cognition/goals.py` | `cognition_traits_goals.go` | Goal extraction/ratification/persistence. |
| `cognition/clustering.py` | `cognition_context_passes.go` | Domain labeling of communities. |
| `cognition/consolidation.py` | `cognition_consolidation.go` | Repeated-fact and stagnant-edge abstraction with provenance. |
| `cognition/prediction.py` | `cognition_context_passes.go` | Per-group prediction bundle. |
| `cognition/self_awareness.py` | `cognition_self_awareness.go` | Self episodes, traits, beliefs, patterns and self-model reads. |
| `cognition/_writes.py` | `cognition_writes.go` | Stable synthetic entity/edge writes. |
| `cognition/_jsonio.py` | `cognition_json.go` | Loose JSON extraction and snake-case normalization. |
| `cognition/prompts.py` | `cognition_prompts.go` | Preserve prompt contracts for injected LLM implementations. |

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

## Acceptance contract

Changes to the frozen schema, persisted identity, prompt semantics, temporal lineage, visibility, or cognition ordering require a new parity review. A similarly named function alone is not evidence of equivalence. Keep the cross-language workflow runnable and extend focused fixtures when changing these contracts.

Use real integration behavior first, targeted regressions for dangerous invariants second, and static checks as supporting evidence. Full deployment certification also requires the actual embedding model/dimension, LLM endpoints, database access policy, load, and failure/recovery conditions used by the application.
