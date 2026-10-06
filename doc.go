// Package surriti provides temporal knowledge graphs and participant-scoped
// memory backed by SurrealDB.
//
// Construct a driver with DefaultDriverConfig and NewDefaultSurrealDriver, then
// pass it to NewSurriti. NewSurritiFromEnv is available for applications that use
// SURRITI_SURREAL_* environment variables. Constructors do not open connections;
// Connect authenticates, initializes the schema, and starts background cognition.
// Close drains background work and closes the driver. Applications own injected
// provider clients and should configure them before sharing a Surriti instance.
//
// Surriti supports episode and triplet ingestion, current and historical graph
// reads, search and recall, participant visibility, resources, communities,
// cognition, and portable memory packs. LLMClient, Embedder, CrossEncoder, and
// Queryer allow callers to supply their own implementations. Implementations
// must support concurrent calls and honor context cancellation.
//
// Without explicit providers, NewSurriti uses deterministic dummy implementations.
// Configure real providers for semantic extraction and embedding, with the same
// embedding dimension in the driver and embedder. Use model constructors and
// DefaultSearchConfig or DefaultCognitionConfig when customizing defaults.
//
// Errors wrap ErrConfig, ErrConnection, ErrSchema, ErrLLM, or ErrNotFound where
// applicable and can be inspected with errors.Is. Logging is silent until the
// application calls SetupLogging.
package surriti
