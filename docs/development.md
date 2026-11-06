# Development

[Home](../README.md)

Mini Harbor uses CNCF Distribution (`registry:3`), a Go API, SQLite with Ent, and a React + TypeScript + Vite UI built with shadcn/ui components.

Requirements: Go 1.26.10 or later, a C compiler for SQLite, Node.js 24, npm, Docker, Docker Compose, and Task.

Run these commands from the repository root:

```bash
docker compose up -d registry
npm ci --prefix web
task dev
```

Run all checks:

```bash
task test
```

Regenerate Ent code after changing `internal/ent/schema`:

```bash
go generate ./internal/ent
```
