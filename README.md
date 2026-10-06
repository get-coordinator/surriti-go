# surriti-go

Go port of [the-hack-foundation/surriti](https://github.com/the-hack-foundation/surriti).

## Port contract

This repository is a behavior-preserving port, not a redesign.

Reference implementation:

- Repository: `the-hack-foundation/surriti`
- Branch: `main`
- Baseline commit: `3e4a26d8e624f60bc3e58581a9336e5fb569aab4`
- Python package version: `0.5.0` plus all behavior present on the baseline commit
- Persistence: SurrealDB 3.x
- Go module: `github.com/get-coordinator/surriti-go`

The Python and Go implementations must remain schema-compatible and must be able to operate against the same SurrealDB data. Existing data is not disposable migration input.

## Implementation and validation

Every production Python module has a Go home; the capability map and compatibility contract are in [docs/PARITY.md](docs/PARITY.md). The implementation keeps the Python SurrealDB schema and temporal history model. There is no alternate persistence layer.

The suite includes live SurrealDB 3.0.5 tests and a Python/Go interoperability workflow covering shared entity identity, timestamp precision, temporal replacement, historical reads, and memory packs in both directions. Real hosted LLM/embedding quality, deployment load, and prolonged network outages still require environment-specific validation.

## Architecture

The SDK uses one `surriti` package. Cohesive files keep ownership visible without adding interfaces or circular package dependencies:

- `surriti.go`, `options.go`: construction, connection and background-work ownership.
- `ingest.go`, `graph_write.go`, `graph_api.go`, `graph_state.go`: ingestion, persistence, current and historical reads.
- `participant_memory.go`: reference writes, authorization, participant recall, grants and revocation.
- `relation_frames.go`, `temporal.go`: deterministic identity and temporal lineage.
- `driver.go`, `driver_surrealdb.go`, `schema.go`: reconnect policy, SDK wire normalization, exact schema and backfills.
- `search*.go`, `recall.go`, `memory_retrieval.go`, `rerank.go`: retrieval and relevance admission.
- `cognition_*.go`: scheduler, ordered fail-soft passes, and prompt contracts.
- `llm_clients.go`, `llm_prompts.go`, `embedder.go`, `provider_http.go`: provider adapters and shared HTTP policy.
- `profiles.go`, `resources.go`, `communities.go`, `read_models.go`, `diagnostics.go`, `memory_pack*.go`: supporting memory capabilities.

Configure providers and options before sharing a `Surriti` instance. Provider implementations and injected `Queryer` implementations must support concurrent calls and context cancellation. `Close` drains/cancels owned work before closing the driver; a provider that ignores cancellation can prevent shutdown from finishing. Applications own injected provider clients and may share them across instances.

Use constructors for models and `DefaultSearchConfig()` / `DefaultCognitionConfig()` when customizing settings: Go zero values do not encode Python keyword defaults. Basic `Search` returns fact results; use `SearchAdvanced` for combined node, episode and community retrieval. `SearchCompat` retains Graphiti argument mapping.

## Checks

```sh
go fmt ./...
go vet ./...
go test ./...
go test -race ./...
```

For live checks, set `SURRITI_INTEGRATION=1` and the usual `SURRITI_SURREAL_URL`, `SURRITI_SURREAL_NS`, `SURRITI_SURREAL_USER`, and `SURRITI_SURREAL_PASS`. Tests always create and remove a uniquely named test database; they never use `SURRITI_SURREAL_DB`. The database account needs permission to create/remove those test databases.

To include the cross-language check, set `SURRITI_PYTHON_REFERENCE` to the directory containing the frozen Python `surriti` package and install its dependencies in the `python3` environment. Run the same `go test -race ./...` command. No hosted provider credentials are required: live database tests use deterministic providers.

OpenAI-compatible and Anthropic adapters accept an injected `HTTPClient` for transport/timeout configuration and `MaxRetries` (constructors default to two). Custom base URLs stay entirely within adapters. Configure the embedding dimension consistently in the driver and embedder.

## Priorities

1. Correctness and data safety.
2. Stable, predictable production behavior.
3. Feature parity with the Python implementation.
4. Deterministic and differential testing.
5. Performance only after the first four are satisfied.

No architectural upgrades, new memory semantics, OpenRouter coupling, Coordinator coupling, JEPA/JEV behavior, or speculative refactors belong in the parity phase.

## Provider boundaries

Surriti depends on capabilities, not vendors:

- `LLMClient`
- `Embedder`
- `CrossEncoder`

Coordinator or another caller injects implementations. Surriti itself must not know or care whether the implementation uses OpenRouter, a local model, a hosted provider, or a deterministic test double.

## Port acceptance

A capability is considered ported only when:

1. Its Python behavior is inventoried.
2. Equivalent Go behavior is implemented.
3. Unit tests cover the same invariants.
4. Live SurrealDB integration tests pass.
5. Python/Go differential fixtures produce equivalent persistent graph state and observable results.
6. Restart, concurrency, retry, and idempotency behavior is verified where applicable.

See [docs/PARITY.md](docs/PARITY.md) for the complete map.
