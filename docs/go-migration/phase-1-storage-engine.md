# Phase 1: storage engine in Go

**Status:** done. The engine is in `go/internal/jsonstore`. 31 Go tests pass with the race detector on macOS; the
Windows and Linux runs happen in CI (a Go job was added to `ci.yml`).

## Goal

Port `packages/jsonstore` to a standalone Go package with the same behaviour and the same files on disk.

## What was built

- **Atomic writes** (temp file, fsync, rename), a **per-file mutex**, and **stable JSON** output that matches the
  TypeScript version byte for byte (sorted keys in JavaScript's UTF-16 order, two-space indent, JavaScript string
  escaping, JavaScript number formatting).
- **`Store` and `JsonFile[T]`**: versioned envelope, schema migrations with a pre-migration backup, validation hook,
  copy-on-write snapshots (a value returned by `Data()` never changes under the caller), two-file transactions with
  rollback, quarantine of corrupt files with restore from the newest backup, and a refusal to touch files from a newer
  schema.
- **Hand-edit reload** with `fsnotify`: a change is read once the file has been quiet for 80 ms, invalid edits are
  reported with a line number and never replace memory, and the store's own writes are not mistaken for edits.
  Watcher paths are converted to forward slashes, the bug that broke reload on Windows in the Node version.
- **Backups**: streamed zips written to a temp file and renamed into place; a file that vanishes mid-backup fails that
  backup cleanly; newest-first ordering that survives several backups in the same second; restore with a zip-slip guard.
- **Instance lock** (`.lock` with the owner's pid; stale and unreadable locks are taken over), with separate
  process-liveness checks for Unix and Windows.
- **Codec**: AES-256-GCM files (`CKENC1B`) and log lines (`CKENC1:`), a plain codec, and a swappable holder. A locked or
  wrongly keyed file is refused and never quarantined or overwritten.

## How it was tested

- All the storage cases from the TypeScript suite were ported (concurrent writers, invalid updates, migrations, newer
  schema, quarantine and restore, defaults when no backup exists, hand-edit reload, own-write detection, rollback, path
  traversal) plus codec, tamper, wrong-key, lock, backup-ordering and zip-slip tests.
- **Cross-compatibility with Node, both ways.** `fixtures/compat/node` was written by the TypeScript engine and the Go
  tests open it (plain, encrypted, encrypted log lines, a Node-made backup restore). `fixtures/compat/go` was written by
  Go and the Node tests open it. Plain Go output is byte-for-byte identical to Node's, including Unicode, U+2028, emoji
  and escapes. Encrypted files from either side decrypt on the other with the shared test key.

## Found along the way

These were bugs or gaps in the **Node** version that the port exposed. They are fixed there too, with tests:

- A file vanishing during a backup raised an uncaught error and could crash the app.
- Two backups made in the same second sorted as the wrong age, so "keep the newest N" could delete a fresh backup.

Porting notes for the later phases: a nil Go slice writes `null` where TypeScript writes `[]` (lists must be
initialised), and an `Open` of the same path with a different type is an error rather than a silent cast.

## Compared with Node + Electron

Measured on the reference machine with the same on-disk data for both engines (`scripts/bench-store.ts` and
`go/cmd/bench-store`): 100 shards holding 5,000 commands and 10,000 Markdown notes (11.2 MB), each plain and
encrypted. Every figure is a range over the rounds that were run. The machine was in normal use during the runs and
timings moved a lot from round to round, so only differences that held in every round are called results.

| Metric                                 | Node          | Go                                                | Result                                                                                                                                                                                                                                                                                  |
| -------------------------------------- | ------------- | ------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Memory after loading the library (RSS) | 189 to 224 MB | 30 to 34 MB                                       | **Go uses about 6 to 7 times less.** Held in every run.                                                                                                                                                                                                                                 |
| Update and write one shard (median)    | 4.0 to 4.7 ms | 4.0 to 6.2 ms                                     | Tie.                                                                                                                                                                                                                                                                                    |
| Open 100 shards (5,000 commands)       | 28 to 54 ms   | 35 to 55 ms                                       | Tie, or Node slightly ahead. A first Go version took 62 to 138 ms because it parsed every file twice; a one-pass fast path brought it level.                                                                                                                                            |
| Read and decode 10,000 note files      | 0.5 to 1.2 s  | 0.5 to 2.8 s                                      | **Inconclusive.** Both engines varied by several times between rounds on the copy-and-read test. In the one finished round of a calmer test (warm files, read in place) Go was 282 and 327 ms against Node's 358 and 355 ms, about 8 to 21% faster. One round is not enough to call it. |
| Zip backup of the whole library        | 1.9 to 3.2 s  | 1.2 to 2.2 s, but 4.6 and 8.3 s in the last round | **Inconclusive.** Go was faster in four rounds, then much slower in the last one, on a busy machine.                                                                                                                                                                                    |

What this phase shows: Go uses far less memory for the same library, and the storage layer is not slower in any way that
held up. It does not show a speed-up. File access cost here is mostly the operating system's, so large gains were not
expected from storage alone; the bigger comparisons are in the search, import and whole-app phases.

Not measured in this phase: cold start of the whole app (needs the Wails shell, phase 5). The read and backup rows
could be re-measured on a quiet machine, with several rounds; that was deliberately not repeated further here.

## Done when

- [x] The Go store opens an unmodified Node-written library, plain and encrypted, and the reverse.
- [x] The ported storage tests pass with the race detector on macOS; CI runs them on macOS, Windows and Linux.
- [x] The benchmark exists for both engines on the same data, and the results are recorded above, including the
      inconclusive rows.
