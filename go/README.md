# Cipherkeep in Go

The Go + Wails port of Cipherkeep. See [docs/go-migration](../docs/go-migration/README.md) for the plan, the phases and
the measured comparison with the Node + Electron version. The Node app in this repository keeps working until the
last phase.

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
| `internal/parity`    | Tests that compare Go with the TypeScript output on shared inputs (phase 3)         |
| `cmd/bench-*`        | Benchmarks that pair with `scripts/bench-*.ts`                                      |

```bash
cd go
go vet ./... && go test -race ./...          # run before every commit
UPDATE_COMPAT=1 go test -run TestCompatWriteGoFixtures ./internal/jsonstore   # regenerate fixtures/compat/go
go run ./cmd/bench-store <dataset-dir>       # see docs/go-migration/phase-1-storage-engine.md
npx tsx scripts/gen-importer-fixtures.ts     # from the repo root: regenerate fixtures/compat/importers (only when TS behaviour changes on purpose)
```

## Compatibility with the Node version

`fixtures/compat` holds files written by each engine. The Go tests read what Node wrote and the Node tests
(`packages/jsonstore/test/go-compat.test.ts`) read what Go wrote, so a library can be opened by either app. Plain files
are byte-for-byte identical. Two porting pitfalls worth knowing: a nil Go slice writes `null` where TypeScript writes
`[]` (initialise lists), and JSON key order follows JavaScript's UTF-16 ordering, which `StableStringify` reproduces.
