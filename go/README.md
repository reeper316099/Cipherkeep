# Cipherkeep in Go

Cipherkeep's backend and desktop shell. It replaced the Node + Electron version in 1.1; see
[docs/go-migration](../docs/go-migration/README.md) for how, and for the measured comparison with the old version.

| Path                 | What it is                                                                          |
| -------------------- | ----------------------------------------------------------------------------------- |
| `internal/jsonstore` | Storage engine: atomic writes, locks, backups, watcher, AES-256-GCM codec (phase 1) |
| `internal/domain`    | Entity model, validation, slugs, wiki links, settings (phase 2)                     |
| `internal/repo`      | Repositories: JSON shards and Markdown notes, including encrypted notes (phase 2)   |
| `internal/search`    | Full-text index with the same scoring as the Node version (phase 2)                 |
| `internal/links`     | Relations, backlinks and the graph (phase 2)                                        |
| `internal/study`     | Spaced repetition, cards, skill map, recommendations (phase 2)                      |
| `internal/library`   | Opens a whole library and wires the above together (phase 2)                        |
| `internal/notion`    | Notion export parsing: names, pages, tables, relations, dates (phase 3)             |
| `internal/csvx`      | CSV reader and writer that behave like PapaParse (phase 3)                          |
| `internal/importer`  | Planners, commit, undo and previews for every import source (phase 3)               |
| `internal/exporter`  | Markdown vault, CSV and JSON export (phase 3)                                       |
| `internal/starter`   | The embedded starter pack (phase 3)                                                 |
| `internal/notionapi` | Optional Notion API import (phase 3)                                                |
| `internal/sanitize`  | Writeup redaction (phase 3)                                                         |
| `internal/parity`    | Tests that compare Go with results recorded from the TypeScript version (phase 3)   |
| `internal/vault`     | Encryption keys: passphrase, recovery key, scrypt, attempt limiting (phase 4)       |
| `internal/api`       | The `/api/v1` HTTP handler, locked and open, with the vault routes (phase 4)        |
| `internal/desktop`   | Desktop shell logic: data folder, safe links, window placement (phase 5)            |
| `app`                | The Wails desktop app: window, menu, bridge for the UI (phase 5)                    |
| `cmd/ck-server`      | The API on a loopback port, for development and the contract tests (phase 4)        |
| `cmd/bench-*`        | Benchmarks used for the migration measurements; each takes a library folder         |

```bash
cd go
go vet ./... && go test -race ./...          # run before every commit
UPDATE_COMPAT=1 go test -run TestCompatWriteGoFixtures ./internal/jsonstore   # regenerate fixtures/compat/go
go run ./cmd/bench-store <dataset-dir>       # see docs/go-migration/phase-1-storage-engine.md
npm run app:build                            # from the repo root: build the desktop app (needs the Wails CLI)
npm test                                     # from the repo root: includes the API contract tests against the Go server
```

## Compatibility with the Node version

`fixtures/compat` holds what the TypeScript version wrote and returned, recorded before it was removed: storage files
(`node`), importer and library results (`importers`, `parity`), vaults (`vault/node`), and two whole libraries written
by the Node app with what its API answered for them (`node-app`, one of them encrypted). The Go tests check against
all of them, so a library made by the old app keeps opening unchanged. The scripts that recorded them were removed with
the Node code (they are in the git history, except the one-off script for `node-app`, which was never committed); the
fixtures are now fixed records, not regenerated.

Two porting pitfalls worth knowing: a nil Go slice writes `null` where TypeScript writes `[]` (initialise lists), and
JSON key order follows JavaScript's UTF-16 ordering, which `StableStringify` reproduces.
