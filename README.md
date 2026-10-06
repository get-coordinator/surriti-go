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
- Go module: `github.com/milorddev/surriti-go`

The Python and Go implementations must remain schema-compatible and must be able to operate against the same SurrealDB data. Existing data is not disposable migration input.

## Current status

Runtime source parity against the frozen Python baseline is complete. The remaining work is environment certification and debugging against real SurrealDB and real provider endpoints. Python's `testing.py` fake driver is intentionally excluded from runtime parity; Go uses native test doubles instead.

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
