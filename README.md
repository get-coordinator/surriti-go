# surriti-go

A Go library for temporal knowledge graphs and participant-scoped memory backed by SurrealDB 3.x. Includes episode/triplet ingestion, search and recall, historical reads, cognition, resources, communities, and portable memory packs.

## Install

Requires Go 1.23 or newer and a running SurrealDB 3.x instance.

```sh
go get github.com/get-coordinator/surriti-go
```

The import path is `github.com/get-coordinator/surriti-go`; the package name is `surriti`.

## Use from your application

```go
package main

import (
    "context"
    "log"
    "time"

    surriti "github.com/get-coordinator/surriti-go"
)

func main() {
    // Reads SURRITI_SURREAL_URL, SURRITI_SURREAL_NS, SURRITI_SURREAL_DB,
    // SURRITI_SURREAL_USER, and SURRITI_SURREAL_PASS.
    memory, err := surriti.NewSurritiFromEnv(nil)
    if err != nil {
        log.Fatal(err)
    }
    defer func() {
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        if err := memory.Close(ctx); err != nil {
            log.Print(err)
        }
    }()

    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    if _, err := memory.Connect(ctx); err != nil {
        log.Print(err)
        return
    }
    result, err := memory.AddTriplet(ctx, surriti.AddTripletRequest{
        SubjectName: "Alice", Predicate: "lives_in",
        ObjectName: "Philadelphia", GroupID: "my-app",
    })
    if err != nil {
        log.Print(err)
        return
    }
    log.Printf("stored %d facts", len(result.Edges))
}
```

Nil options use deterministic dummy LLM and embedding implementations. For semantic extraction, inject your providers through `SurritiOptions{LLM: llm, Embedder: embedder}`. Built-in adapters are available through `NewOpenAILLMClient`, `NewAnthropicLLMClient`, and `NewOpenAIEmbedder`; custom implementations satisfy the same small interfaces. Configure the driver and embedder with matching dimensions (driver default: 768; OpenAI embedder default: 1536).

For explicit configuration, start with `DefaultDriverConfig()`, construct a driver with `NewDefaultSurrealDriver(cfg)`, then call `NewSurriti(driver, options)`. Construction does not connect. `Connect` initializes the schema and starts background work; `Close` drains that work and closes the driver. Injected providers remain application-owned. Configure instances before using them concurrently, and honor cancellation in custom providers.

Use a consistent `GroupID` to isolate application memory. Inspect error categories with `errors.Is(err, surriti.ErrConnection)` and the other exported sentinels. Logging is silent by default; call `SetupLogging` to opt in.

See [compiled examples](example_test.go) and `go doc github.com/get-coordinator/surriti-go` for the public API.

## Port contract

The library preserves the behavior of [the-hack-foundation/surriti](https://github.com/the-hack-foundation/surriti), while organizing implementation details for Go consumers.

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

The public API lives in the root `surriti` package, so applications use one import. Internal packages isolate transport implementation details without exposing additional APIs or introducing dependencies back into the graph layer:

- `surriti.go`, `options.go`: construction and lifecycle ownership.
- `ingest.go`, `graph_*.go`, `participant_memory.go`: writes, temporal reads, and participant authorization.
- `driver.go`, `schema.go`: connection/retry policy, schema and backfills.
- `internal/surrealtransport`: official SurrealDB SDK adapter and wire normalization.
- `internal/providerhttp`: shared provider HTTP retry, cancellation, and bounded response policy.
- `llm.go`, `embedder.go`, `rerank.go`: injectable capabilities and deterministic implementations.
- `provider_openai.go`, `provider_anthropic.go`, `provider_openai_embedding.go`: vendor protocols and configuration.
- `llm_clients.go`, `llm_parse.go`, `llm_prompts.go`: shared model operations, parsing and prompt contracts.
- `search*.go`, `recall.go`, `memory_retrieval.go`: retrieval and relevance admission.
- `cognition_*.go`: scheduler and ordered cognition passes.
- `profiles.go`, `resources.go`, `communities.go`, `read_models.go`, `diagnostics.go`, `memory_pack*.go`: supporting capabilities and their facade methods.

Graph and cognition code share the same models and lifecycle; they remain together rather than requiring public forwarding layers. Existing exported names, signatures, and compatibility aliases remain available.
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

Library refactors must preserve the public API, schema, memory semantics, prompt contracts, and interoperability. Keep application-specific routing and behavior in the consuming application.

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
