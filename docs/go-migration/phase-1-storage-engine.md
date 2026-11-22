# Phase 1: storage engine in Go

**Status:** planned. Nothing on this page has been measured yet.

## Goal

Port `packages/jsonstore` to a standalone Go package with the same behaviour and the same files on disk.

## Scope

Atomic writes, a per-file mutex, the JSON shard envelope and schema migrations, hand-edit reload, quarantine and restore from backup, rolling zip backups, the single-instance lock, two-file transactions with rollback, and the AES-256-GCM codec (`CKENC1B` files, `CKENC1:` log lines) with scrypt key wrapping.

## Design

- `go/internal/jsonstore`, no dependency on the rest of the app (it stays reusable).
- Standard library for files, zip (`archive/zip`, streamed) and crypto (`crypto/aes`, `crypto/cipher`); `fsnotify` for the watcher; `golang.org/x/crypto/scrypt` for the key.
- Paths are normalised to forward slashes before they are used as keys (the Windows bug fixed in the Node version).
- A file that disappears mid-backup fails that backup, it never crashes.
- A wrong key or locked store never quarantines or overwrites data.

## How it will be tested

- Golden-file tests: libraries written by the Node version (plain and encrypted) must load, and files written by Go must load in Node.
- The same cases as `packages/jsonstore/test` (atomic write, concurrent writers, invalid hand edits, rollback, backup pruning, codec tamper and wrong-key tests), ported.
- `go test -race`, and the Windows and Linux CI runners from the start.
- Benchmark: load 10k notes and 5k commands, then write one note.

## Compared with Node + Electron

Filled in when the phase is finished, from measurements made the way [phase 0](phase-0-baseline-and-scaffold.md)
describes. If Go is not better on a row, or the difference is within noise, this page will say so.

| Metric                                        | Node + Electron (baseline)                          | Go + Wails       | Change |
| --------------------------------------------- | --------------------------------------------------- | ---------------- | ------ |
| Start: load and index 10k notes + 5k commands | 5,450 ms, 281 MB heap                               | not measured yet |        |
| Write one note (create or patch)              | 18 to 76 ms                                         | not measured yet |        |
| Read one note                                 | 0.3 ms (through the API)                            | not measured yet |        |
| Memory with that library                      | 281 MB heap, about 520 MB for the whole app at idle | not measured yet |        |

## Done when

- [ ] The Go store opens an unmodified Node-written library, plain and encrypted, and the reverse.
- [ ] All ported storage tests pass with `-race` on macOS, Windows and Linux.
- [ ] The 10k-note benchmark exists and results are recorded here.

## Risks

- File watching differs per OS. The Windows path-separator bug is the kind of thing to test on a Windows runner early.
