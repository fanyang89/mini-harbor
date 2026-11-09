# Development

[Home](../README.md)

Mini Harbor uses CNCF Distribution (`registry:3`), a Go API, rqlite via its HTTP SQL API, and a React + TypeScript + Vite UI built with shadcn/ui components.

Requirements: Go 1.26.10 or later, Node.js 24, npm, Docker, Docker Compose, and Task. The application builds with CGO disabled; race-enabled tests require a C compiler.

Run these commands from the repository root:

```bash
docker compose -f compose.yml -f deploy/poc/rqlite/compose.dev.yml up -d registry rqlite
npm ci --prefix web
task dev
```

Run all checks:

```bash
task test
```

Run real database integration tests (including the organization HTTP API and three-node failover):

```bash
task poc:rqlite
```

Plain `go test ./...` runs unit tests and skips Docker-backed tests unless
`RQLITE_TESTS=1` is set. CI enables them. Test clusters are disposable and do not
use the development database. `task poc:rqlite` also runs the standalone Python
SQL/HA probe; Python 3 is required for that command.

Organization storage is implemented in `internal/organization`. Add explicit,
idempotent SQL migrations in `migrate.go`; each migration and its version marker
are committed together. There is no Ent generation or alternate SQLite backend.
