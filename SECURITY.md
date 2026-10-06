# Security Policy

## Reporting a vulnerability

Please do not open a public GitHub issue for a suspected security vulnerability.

Use GitHub Private Vulnerability Reporting for this repository when it is available. If private vulnerability reporting is unavailable, contact the repository maintainers privately through the get-coordinator organization before publishing details.

Include enough information to reproduce and assess the issue, including:

- affected version or commit
- impact
- reproduction steps
- relevant configuration
- whether production data or credentials may be exposed

Please avoid including real API keys, database credentials, user memory, or other sensitive data in reports.

## Security-sensitive areas

Changes involving the following areas deserve additional review:

- participant and group authorization
- tenant isolation
- SurrealDB queries and schema migration
- memory-pack archive handling
- provider HTTP clients
- retry/reconnect behavior
- destructive database operations
- persistence identity and temporal history

## Credentials

Surriti does not require credentials to be stored in source code. Applications should provide database and model-provider credentials through their normal secret-management system or process environment.
