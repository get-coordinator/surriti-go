# Surriti Go

Surriti is a Go library for durable, temporal graph memory backed by SurrealDB 3.x.

It provides episode and fact ingestion, entity resolution, temporal truth, participant-scoped memory, hybrid search and recall, cognition, communities, resources, diagnostics, and portable memory packs behind a single Go package.

## Highlights

- SurrealDB 3.x as the source of truth
- Temporal facts with correction, supersession, history, and as-of reads
- Entity and alias resolution
- Participant-scoped memory visibility and sharing
- Vector + full-text + hybrid search and recall
- Optional cognition, reinforcement, decay, consolidation, traits, goals, and self-awareness
- Provider-neutral LLM, embedding, and reranking interfaces
- Built-in OpenAI-compatible, OpenAI embedding, and Anthropic adapters
- Portable memory-pack import/export
- Context-aware lifecycle, retry, reconnect, and graceful shutdown behavior

Surriti is a library, not a service. Applications import it directly and own its lifecycle.

## Requirements

- Go 1.23 or newer
- SurrealDB 3.x
- An embedding model whose output dimension matches the configured SurrealDB vector dimension
- An LLM only when using natural-language extraction, contradiction analysis, relation classification, cognition, or other model-backed behavior

## Install

~~~bash
go get github.com/get-coordinator/surriti-go
~~~

Import the package as:

~~~go
import surriti "github.com/get-coordinator/surriti-go"
~~~

For production applications, pin a release tag or a specific commit rather than implicitly tracking main.

## Start SurrealDB locally

A minimal local SurrealDB 3.x instance:

~~~bash
docker run --rm --name surriti-surreal \
  -p 8000:8000 \
  surrealdb/surrealdb:v3.0.5 \
  start --user root --pass root memory
~~~

The default Surriti endpoint is ws://localhost:8000/rpc.

## Quick start

This example uses direct triplet ingestion, so it does not require an external LLM.

~~~go
package main

import (
	"context"
	"log"
	"time"

	surriti "github.com/get-coordinator/surriti-go"
)

func main() {
	cfg := surriti.DefaultDriverConfig()
	cfg.Username = "root"
	cfg.Password = "root"

	driver, err := surriti.NewDefaultSurrealDriver(cfg)
	if err != nil {
		log.Fatal(err)
	}

	memory, err := surriti.NewSurriti(driver, nil)
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := memory.Connect(ctx); err != nil {
		log.Fatal(err)
	}

	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = memory.Close(closeCtx)
	}()

	result, err := memory.AddTriplet(ctx, surriti.AddTripletRequest{
		SubjectName: "Alice",
		Predicate:   "lives_in",
		ObjectName:  "Philadelphia",
		GroupID:     "example",
	})
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("stored %d fact(s)", len(result.Edges))
}
~~~

## Environment configuration

Surriti can construct its SurrealDB driver from environment variables:

| Variable | Default | Purpose |
| --- | --- | --- |
| SURRITI_SURREAL_URL | ws://localhost:8000/rpc | SurrealDB RPC endpoint |
| SURRITI_SURREAL_NS | surriti | Namespace |
| SURRITI_SURREAL_DB | surriti | Database |
| SURRITI_SURREAL_USER | empty | Username |
| SURRITI_SURREAL_PASS | empty | Password |
| SURRITI_EMBEDDING_DIM | 768 | Vector dimension |

Then construct with:

~~~go
memory, err := surriti.NewSurritiFromEnv(nil)
~~~

The repository includes a .env.example with safe placeholders for local development.

## Natural-language ingestion

To extract structured graph memory from text, inject an LLM and an embedder.

Surriti includes an OpenAI-compatible adapter. That means OpenAI-compatible providers can be used by supplying their base URL without adding provider-specific behavior to Surriti itself.

Example using OpenRouter for both chat and embeddings:

~~~go
apiKey := os.Getenv("OPENROUTER_API_KEY")
baseURL := "https://openrouter.ai/api/v1"

llm, err := surriti.NewOpenAILLMClient(
	"openai/gpt-4.1-mini",
	apiKey,
	baseURL,
)
if err != nil {
	log.Fatal(err)
}

embedder, err := surriti.NewOpenAIEmbedder(
	"openai/text-embedding-3-small",
	768,
	apiKey,
	baseURL,
)
if err != nil {
	log.Fatal(err)
}

cfg := surriti.DefaultDriverConfig()
cfg.EmbeddingDim = 768

driver, err := surriti.NewDefaultSurrealDriver(cfg)
if err != nil {
	log.Fatal(err)
}

memory, err := surriti.NewSurriti(driver, &surriti.SurritiOptions{
	LLM:      llm,
	Embedder: embedder,
})
if err != nil {
	log.Fatal(err)
}
~~~

Then ingest a natural-language episode:

~~~go
speakerID := "user-123"
speakerName := "Alice"

result, err := memory.AddEpisode(ctx, surriti.AddEpisodeRequest{
	Name:              "conversation-turn-1",
	EpisodeBody:       "I moved to Seattle last year and now work at Acme.",
	GroupID:           "user-123",
	Source:            surriti.EpisodeMessage,
	SourceDescription: "chat",
	SpeakerID:         &speakerID,
	SpeakerName:       &speakerName,
})
~~~

## Embedding compatibility

Embedding dimension and embedding model identity are persistent data concerns.

The configured driver dimension must match the embedder output dimension. More importantly, do not mix vectors from different embedding models in the same existing graph merely because they have the same dimension. A 768-dimensional vector from one model is not semantically compatible with a 768-dimensional vector from another model.

Changing embedding models for an existing database generally requires re-embedding stored vectors.

## Provider interfaces

Applications may provide their own implementations:

~~~go
type LLMClient interface {
	Extract(context.Context, ExtractionRequest) (ExtractionResult, error)
	FindContradictions(context.Context, ContradictionRequest) ([]int, error)
}

type Embedder interface {
	EmbeddingDim() int
	Create(context.Context, string) ([]float64, error)
}

type CrossEncoder interface {
	Rank(context.Context, string, []string) ([]int, error)
}
~~~

See the exported interfaces in the package for the exact current contracts.

Built-in adapters include:

- NewOpenAILLMClient
- NewAnthropicLLMClient
- NewOpenAIEmbedder

Custom base URLs stay at the adapter boundary. Core graph and memory behavior has no dependency on OpenRouter, OpenAI, Anthropic, or any application-specific service.

## Lifecycle

Construction does not open a database connection.

Call Connect once during application startup:

~~~go
if _, err := memory.Connect(ctx); err != nil {
	return err
}
~~~

Call Close during shutdown:

~~~go
closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()

if err := memory.Close(closeCtx); err != nil {
	log.Print(err)
}
~~~

Connect initializes the managed schema and starts configured background cognition. Close drains background work before closing the shared database transport.

Injected provider clients remain application-owned.

## Tenancy and groups

GroupID is the primary memory isolation boundary. Applications should derive it from trusted application state rather than arbitrary client input.

Participant-scoped APIs add a second authorization layer for memories that are asserted, witnessed, granted, endorsed, or disputed by specific participants.

## Persistence and temporal semantics

Surriti preserves temporal history instead of destructively overwriting prior facts.

The graph supports operations including:

- assert
- correct
- terminate
- qualify
- noop

Current-state reads and historical/as-of reads are separate operations. Corrections and singleton replacements preserve lineage through validity intervals and supersession metadata.

## Development

Run the normal checks with:

~~~bash
go fmt ./...
go vet ./...
go test ./...
go test -race ./...
~~~

Live SurrealDB integration tests are opt-in:

~~~bash
export SURRITI_INTEGRATION=1
export SURRITI_SURREAL_URL=ws://127.0.0.1:8000/rpc
export SURRITI_SURREAL_NS=surriti_go
export SURRITI_SURREAL_USER=root
export SURRITI_SURREAL_PASS=root

go test -race ./...
~~~

Integration tests create and remove uniquely named test databases and do not use SURRITI_SURREAL_DB.

## Compatibility history

This Go implementation was developed as a behavioral port of an earlier Python Surriti implementation. The compatibility work covers schema behavior, temporal facts, relation frames, participant memory, search and recall, cognition, memory packs, lifecycle behavior, and Python/Go interoperability.

The detailed engineering contract is kept in [docs/PARITY.md](docs/PARITY.md). It is primarily maintainer documentation rather than required reading for library consumers.

## Security

Do not commit provider keys, database credentials, memory exports, or production data.

See [SECURITY.md](SECURITY.md) for vulnerability reporting guidance.

Destructive database clearing is disabled by default and requires SURRITI_ALLOW_DESTRUCTIVE=1.

## Contributing

Contributions are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md).

Behavior that changes persisted identity, schema compatibility, temporal lineage, authorization, or provider contracts should receive additional review because those are compatibility-sensitive surfaces.

## License

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE).
