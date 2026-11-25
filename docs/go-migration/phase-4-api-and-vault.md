# Phase 4: API layer and vault

**Status:** done. The Go API passes the TypeScript API, vault and UI-serving tests (40 tests) unchanged in what they
check, plus its own Go tests with the race detector. CI runs both on macOS, Windows and Linux.

## Goal

Serve the same HTTP-shaped API from Go, and drop the loopback server.

## What was built

| Path             | What it does                                                                                                                                                                 |
| ---------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `internal/vault` | Encryption keys: a random master key wrapped under the passphrase and under a recovery key (scrypt, AES-256-GCM), passphrase change, new recovery key, the attempt limiter   |
| `internal/api`   | Every `/api/v1` route, request validation, the shared error shape, live events (SSE), attachments, backups, settings, import and export, and the locked and open states      |
| `cmd/ck-server`  | The same handler on a loopback port with a per-launch token, for development and for running the TypeScript tests against Go. Prints `{url, token}`, stops when stdin closes |

## Design notes

- **One `http.Handler`, two states.** `api.Server` holds the instance lock and the vault and serves either the open API
  or the small locked one (health, vault status, unlock, and `423 LOCKED` for everything else). Unlocking builds the open
  handler and swaps it in; locking swaps back, closes the library (which ends open event streams) and drops the key.
  While encryption is being switched on or off, other API requests get `503 BUSY`, as in Node.
- **No port in the desktop app.** Phase 5 mounts `api.Server` on the Wails asset server, so the UI's
  `fetch('/api/v1/...')` is answered inside the process. With no socket there is nothing for another program to reach,
  so in that mode there is no token and no Host check. With a token set (the loopback listener), both guards apply exactly
  as in Node: loopback Host headers only, and the bearer token on everything under `/api`.
- **The standard router.** Go 1.22's pattern router (`GET /api/v1/commands/{id}`), wrapped so that an unknown route or a
  wrong method gets the Node server's JSON 404 instead of Go's plain-text 404 or 405.
- **Same files on disk.** `vault.json` is read and written by both versions: a Node-made vault unlocks in Go (passphrase
  and recovery key), and a Go-made one unlocks in Node (`fixtures/compat/vault`).

### Where Go behaves differently on purpose

- **Validation details.** Errors have the same shape (`{error: {code, message, requestId, details}}`) and the same status
  and code. The `details` of a `VALIDATION` error is a list of `{code, path, message}` like Zod's issues, but the wording
  is not Zod's. The UI does not read `details`.
- **A few Fastify error codes** (an empty or broken JSON body, a wrong content type, an oversized upload) are reproduced
  by code and status, not by message.
- **The JSON export** is indented the same way, but its keys are in a different order (Go sorts map keys). It re-imports
  identically; this was already the case in phase 3.

## How it was tested

- **The TypeScript tests, run against Go.** `npm run test:go-contract` runs `api.test.ts`, `vault.test.ts` and
  `static.test.ts` against `ck-server` instead of the Node server (the harness switches on `CK_SERVER=go`). That covers
  auth and the Host guard, errors, create, read, list, patch, delete and bulk for every collection, shard and note layout,
  activity, search with operators, tags, templates, strict settings, backup and restore, server-sent events, and the
  whole vault flow: no plaintext on disk or in backups after enabling, restart locked, wrong passphrase, recovery with a
  new passphrase, passphrase change without rewriting data, a plaintext backup restored into an encrypted library, lock
  now, the attempt limiter, and disabling back to the original layout. All 40 pass. The only test edits: the server is
  started through a helper, and "is it locked?" is asked over HTTP (`GET /vault`) instead of reading a Node object.
- **The contract test really tests Go.** Changing the Go Host-guard status from 403 to 402 makes it fail.
- **Go tests** (`internal/api`, `internal/vault`): the in-process handler works with no token and any Host; the loopback
  guards; JSON 404 for unknown routes and wrong methods; the vault flow with a check that every file and every backup
  entry is ciphertext; `503 BUSY` during an encryption change; the event stream; the limiter's timing (five free tries,
  then 1, 2, 4, 8, 16 and 30 s); passphrase length counted in UTF-16 units like JavaScript; a damaged `vault.json`
  reported as damaged, not as a wrong passphrase.
- **Not ported:** `persistence.test.ts` reaches into the Node server's internals (its event bus), so it stays
  Node-only. The same behaviour (hand-edit reload, the single-instance lock) is covered by the Go tests from phases 1 and 2.
  The Notion API routes are covered by the phase 3 Go tests of the job runner; the route test with a fake Notion
  (`notion-api.test.ts`) also replaces Node internals and stays Node-only.

## Compared with Node + Electron

Measured with `scripts/bench-api.ts` and `go/cmd/bench-api` on the same on-disk library as phase 2 (10,000 notes, 5,000
commands, 200 concepts, 100 labs, 150 log entries), with the requests of `npm run bench`. Node is measured over loopback
HTTP, as the Electron app calls it. Go is measured over loopback too (the fair comparison) and in process (how the
Wails app will call it). Each figure is the median of three rounds in one run, with the range in brackets. File watching
was off in both. The machine was in normal use.

| Metric                                    | Node, loopback         | Go, loopback           | Go, in process   | Result (loopback against loopback)                                                            |
| ----------------------------------------- | ---------------------- | ---------------------- | ---------------- | --------------------------------------------------------------------------------------------- |
| One note `GET /notes/:id`                 | 0.29 ms (0.27 to 0.32) | 0.10 ms (0.09 to 0.10) | 0.01 ms          | **Go about 3x faster.** In process, the HTTP cost disappears.                                 |
| List 50 notes                             | 6.3 ms (6.0 to 6.5)    | 4.0 ms (4.0 to 4.2)    | 3.9 ms           | **Go about 1.6x faster.**                                                                     |
| List 500 notes, full bodies               | 8.7 ms (8.3 to 8.7)    | 6.9 ms (6.6 to 8.1)    | 6.1 ms           | Go about 1.3x faster.                                                                         |
| List 500 notes without bodies             | 7.1 ms (6.9 to 7.5)    | 5.7 ms (5.2 to 5.9)    | 5.0 ms           | Go about 1.25x faster.                                                                        |
| List 500 commands                         | 3.2 ms (3.0 to 3.3)    | 4.5 ms (4.4 to 4.5)    | 4.0 ms           | **Node is faster, about 1.4x.** Not investigated yet.                                         |
| Search `nmap`, all types                  | 8.6 ms (8.0 to 9.1)    | 2.6 ms (2.6 to 2.8)    | 2.6 ms           | **Go about 3.3x faster.**                                                                     |
| Search `-sV`                              | 15.9 ms (15.0 to 15.9) | 3.2 ms (3.2 to 3.3)    | 3.1 ms           | **Go about 5x faster.**                                                                       |
| Search prefix `kern`, notes only          | 12.6 ms (12.2 to 13.1) | 3.2 ms (3.2 to 3.3)    | 3.2 ms           | **Go about 3.9x faster.**                                                                     |
| Search `cmd:cmd42`                        | 1.2 ms (0.9 to 1.2)    | 1.7 ms (1.7 to 1.7)    | 1.6 ms           | **Node is faster, about 1.4x** (the exact-name lookup found in phase 2).                      |
| Graph                                     | 7.8 ms (7.7 to 8.3)    | 6.1 ms (5.4 to 10.9)   | 4.8 ms           | Go a little faster; the Go range is wide.                                                     |
| Dashboard `GET /stats/overview`           | 38.4 ms (38 to 48)     | 11.6 ms (11.4 to 12.5) | 13.1 ms          | **Go about 3.3x faster.**                                                                     |
| Edit one note `PATCH`                     | 8.9 ms (8.5 to 13.8)   | 5.3 ms (4.2 to 5.9)    | 5.6 ms           | Go about 1.7x faster.                                                                         |
| Open the library (load and index)         | 3,395 ms               | 676 ms                 |                  | **Go about 5x faster.** One measurement each, see below.                                      |
| Peak memory (RSS) for the whole run       | 876 MB                 | 302 MB                 |                  | **Go uses about 2.9x less.**                                                                  |
| Listening sockets while running           | 1 (plus a token)       | 1 (plus a token)       | **0**            | The desktop app will use the in-process handler: no port at all.                              |
| Unlock an encrypted library (scrypt 2^15) | 70 ms (70 to 72)       |                        | 58 ms (57 to 63) | Go about 17% faster. Almost all of it is scrypt; no big gain was expected, and there is none. |

### About these numbers

- **Open time.** This is the number phase 2 could not give. Here the server is started once per version on the same
  library (load every file, build the search index), with nothing else running in the benchmark: Node 3.4 s, Go 0.68 s.
  It is one start each, not three, so it is a measurement, not yet a range; phase 5 measures the whole app's start.
- **In process against loopback.** For the small calls the socket is most of the cost (one note: 0.10 ms over loopback,
  0.01 ms in process). For the heavier calls the work dominates and the two are close; the in-process dashboard row was
  even slightly slower than loopback in this run, which is noise between rounds rather than a real cost.
- **Unlock** was measured on a small library at the real scrypt cost, so it is the key derivation plus opening a
  near-empty library. Unlocking a large encrypted library also decrypts every file; that is not measured here.
  The old table expected 0.2 to 0.4 s from the test logs; measured on its own it is well under 0.1 s in both versions.
- **Where Node wins:** listing 500 commands and the exact-name search. Both are a millisecond or so, and neither was
  looked into in this phase.

### What this phase shows

Over the same loopback HTTP, the Go API is faster on almost every call, much faster on search and the dashboard, opens
the library about five times faster and uses about a third of the memory. In the desktop app it will not listen on any
port. Node is still a little faster at listing many commands and at exact-name lookups.

## Done when

- [x] Every route behaves the same under the contract tests (the TypeScript suite, run against both servers).
- [x] The vault flow passes the ported tests, including the no-plaintext-on-disk check.
- [x] The in-process handler needs no network port (0 listening sockets, measured).

## Risks that remain

- Server-sent events through the Wails asset server are not tried yet (phase 5). If the web view does not stream, a
  Wails event takes their place.
- During `lock`, a request already being served when the library closes can fail. Node has the same window; it has not
  been seen in tests.
