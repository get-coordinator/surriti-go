package surriti

// Compatibility aliases preserve the conceptual public SDK surface of the
// Python reference while retaining idiomatic Go names.
type Graphiti = Surriti
type EmbedderClient = Embedder
type CrossEncoderClient = CrossEncoder
type ClaimOperation = FactOperation

const (
	MEMORY_PACK_FORMAT  = MemoryPackFormat
	MEMORY_PACK_VERSION = MemoryPackVersion
)

var (
	DEFAULT_FRAMES      = DefaultFrames
	IDENTITY_PREDICATES = IdentityPredicates
)
