# Phase 4: API layer and vault

**Status:** planned. Nothing on this page has been measured yet.

## Goal

Serve the same HTTP-shaped API from Go, and drop the loopback server.

## Scope

All `/api/v1` routes, request validation, uniform errors, the SSE event stream, attachments, backups, settings, and the encryption vault (enable, disable, lock, unlock, change passphrase, recovery key, attempt limiting, `423 Locked`).

## Design

- `net/http` with the Go 1.22 pattern router.
- In the desktop app the API is mounted on the Wails asset server as a handler, so the UI's `fetch('/api/v1/...')` calls are answered inside the process. There is no TCP port and no bearer token to protect, which removes the port-scan and DNS-rebinding questions. A loopback listener stays available for development and tests.
- Live events use an SSE stream the same way; if the web view limits streaming, a Wails event is used instead.
- The vault state machine (locked app, open app, swap on unlock) maps onto a handler swap behind one `http.Handler`.

## How it will be tested

- The existing API test suite is ported route by route, and run against both servers as a contract test.
- The vault tests (ciphertext on disk and in backups, restart locked, wrong passphrase, recovery key, lock now) are ported.
- Benchmark: the same requests as `npm run bench`, in process and over loopback.

## Compared with Node + Electron

Filled in when the phase is finished, from measurements made the way [phase 0](phase-0-baseline-and-scaffold.md)
describes. If Go is not better on a row, or the difference is within noise, this page will say so.

| Metric                                    | Node + Electron (baseline)  | Go + Wails       | Change |
| ----------------------------------------- | --------------------------- | ---------------- | ------ |
| API call, one note (median)               | 0.3 ms over loopback HTTP   | not measured yet |        |
| API call, list 50 notes                   | 9.0 ms                      | not measured yet |        |
| Search `-sV`                              | 18.8 ms                     | not measured yet |        |
| Open sockets while running                | 1 loopback port + token     | not measured yet |        |
| Unlock an encrypted library (scrypt 2^15) | about 0.2 to 0.4 s in tests | not measured yet |        |

## Done when

- [ ] Every route behaves the same under the contract tests.
- [ ] The vault flow passes the ported tests, including the no-plaintext-on-disk check.
- [ ] The in-process handler needs no network port.

## Risks

- Encryption cost is dominated by scrypt, so no large speed-up is expected there. If it is the same, the page will say so.
