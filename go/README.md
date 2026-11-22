# Cipherkeep in Go

The Go + Wails port of Cipherkeep. See [docs/go-migration](../docs/go-migration/README.md) for the plan, the phases and
the measured comparison with the Node + Electron version. The Node app in this repository keeps working until the
last phase.

| Path                 | What it is                                                                          |
| -------------------- | ----------------------------------------------------------------------------------- |
| `internal/jsonstore` | Storage engine: atomic writes, locks, backups, watcher, AES-256-GCM codec (phase 1) |
| `cmd/bench-store`    | Benchmark that pairs with `scripts/bench-store.ts`                                  |

```bash
cd go
go vet ./... && go test -race ./...          # run before every commit
UPDATE_COMPAT=1 go test -run TestCompatWriteGoFixtures ./internal/jsonstore   # regenerate fixtures/compat/go
go run ./cmd/bench-store <dataset-dir>       # see docs/go-migration/phase-1-storage-engine.md
```

## Compatibility with the Node version

`fixtures/compat` holds files written by each engine. The Go tests read what Node wrote and the Node tests
(`packages/jsonstore/test/go-compat.test.ts`) read what Go wrote, so a library can be opened by either app. Plain files
are byte-for-byte identical. Two porting pitfalls worth knowing: a nil Go slice writes `null` where TypeScript writes
`[]` (initialise lists), and JSON key order follows JavaScript's UTF-16 ordering, which `StableStringify` reproduces.
