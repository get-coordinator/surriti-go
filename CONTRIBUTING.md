# Contributing

Thanks for contributing to Surriti Go.

## Development setup

Requirements:

- Go 1.23 or newer
- SurrealDB 3.x for live integration tests

Run the standard checks before submitting changes:

~~~bash
go fmt ./...
go vet ./...
go test ./...
go test -race ./...
~~~

Live SurrealDB tests are opt-in. See the README for the SURRITI_INTEGRATION environment setup.

## Design principles

Please preserve these invariants unless a change explicitly intends to revise the compatibility contract:

- SurrealDB remains the source of truth.
- Existing stored graph data must remain readable across compatible releases.
- Temporal corrections preserve history rather than destructively replacing it.
- Group and participant authorization must be enforced before unrestricted memory is exposed.
- Provider integrations remain behind small interfaces.
- Core Surriti must not depend on an application-specific service or routing provider.
- Retry, reconnect, and shutdown behavior must remain bounded and concurrency-safe.
- Background cognition/profile failures must not corrupt or destabilize base ingest.
- Memory-pack handling must remain deterministic and safe.

Prefer simple, explicit Go over abstraction for its own sake.

## Tests

Add focused regression tests for behavioral changes, especially around:

- deterministic identity
- temporal replacement and as-of reads
- participant authorization
- schema compatibility
- parser behavior
- concurrent writes
- reconnect/shutdown behavior
- archive validation

Do not add real API keys, production database dumps, user memory, or other private data to fixtures.

## Compatibility-sensitive changes

Changes to persisted identity, schema, temporal semantics, prompt contracts, participant visibility, memory-pack formats, or public APIs should be called out clearly in the pull request.

The historical Python-to-Go compatibility map is documented in docs/PARITY.md.

## License

By contributing, you agree that your contributions will be licensed under the Apache License, Version 2.0.
