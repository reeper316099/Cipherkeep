# Moving Cipherkeep to Go + Wails

This folder records the staged move from **Node + Electron** to **Go + Wails**, finished in version 1.1. Each phase has
its own page with what changed, how it was tested, and a **measured** comparison with the Node + Electron version. The
[final comparison](#final-comparison) below collects them.

## Why

- Ship one native binary instead of a Node server plus a bundled Chromium.
- Remove the Node runtime from the running app (smaller installers, less memory, a faster start).
- Keep every feature and every file format. A library written by the Node version must open in the Go version
  and the other way round.

## What stays the same

- **The UI.** Wails shows a web frontend, so the React app in `apps/web` is reused. Changes there are limited
  to how it talks to the backend.
- **The data format.** Same folders, JSON shards, Markdown notes, encrypted-file layout (`CKENC1B`) and
  `vault.json`. See [DATA_FORMAT.md](../DATA_FORMAT.md) and [SECURITY.md](../SECURITY.md).
- **The privacy rules.** No telemetry, no accounts, no network in the app; the Notion API import stays
  optional and off by default.

## What does not go away (be honest about it)

- **Node is still needed to build the frontend** (Vite and the npm packages) on a developer machine and in CI.
  It is no longer part of the running app. Removing it entirely would mean rewriting the UI, which is not
  planned.
- **Wails uses the system web view** (WebKit on macOS, WebView2 on Windows, WebKitGTK on Linux), not a bundled
  Chromium. That is where the size win comes from, and also where small rendering differences between systems
  can appear. The accessibility audit and end-to-end tests were rebuilt for it (phase 7).
- **The TypeScript code was the reference** until the cut-over. What it wrote and returned is kept as fixed records in
  `fixtures/compat`, and the Go tests still check against them.

## How the comparisons are measured

Rules for every "faster / smaller / better" claim in this folder:

1. It is a number measured on the same computer, on the same day, with the same data, for both versions.
2. The method is written next to it so it can be repeated (`npm run bench` for Node; the matching Go
   benchmark for Go).
3. If Go is not better, or the difference is within noise, the page says so.
4. Nothing is filled in before it is measured. A cell that says "not measured yet" means exactly that.

Reference machine for the Node + Electron baseline: MacBook Air, Apple silicon, macOS 27.0.1, Node 22.15.1,
Electron 44.9.0. Timings vary by a lot when other apps are busy, so both versions are measured the same way,
more than once.

## Phases

| Phase | Page                                                                        | Status |
| ----- | --------------------------------------------------------------------------- | ------ |
| 0     | [Baseline and scaffold](phase-0-baseline-and-scaffold.md)                   | Done   |
| 1     | [Storage engine (jsonstore in Go)](phase-1-storage-engine.md)               | Done   |
| 2     | [Repositories, search, links, study engine](phase-2-domain-search-study.md) | Done   |
| 3     | [Import and export](phase-3-import-export.md)                               | Done   |
| 4     | [API layer and vault](phase-4-api-and-vault.md)                             | Done   |
| 5     | [Wails shell and UI](phase-5-wails-shell-and-ui.md)                         | Done   |
| 6     | [Packaging and CI](phase-6-packaging-and-ci.md)                             | Done   |
| 7     | [Parity tests and cut-over](phase-7-parity-and-cutover.md)                  | Done   |

The Node + Electron code was removed in phase 7 and is in the git history. The Go code lives in `go/`.

## Baseline summary (Node + Electron, measured 2026-11-21)

| Metric                                              | Node + Electron                                              |
| --------------------------------------------------- | ------------------------------------------------------------ |
| macOS installer (universal dmg)                     | 231.4 MB                                                     |
| Windows installer (setup / portable)                | 112.9 MB / 112.6 MB                                          |
| Linux (AppImage / deb / rpm)                        | 127.3 MB / 101.1 MB / 90.3 MB                                |
| Idle memory, empty library, 10 s after start        | about 520 MB (4 processes, summed resident memory)           |
| Launch to first screen, empty library, warm         | about 0.67 s                                                 |
| 10k notes + 5k commands: server start (load, index) | about 5.5 s, 281 MB heap                                     |
| Typical API calls with that library                 | search 1 to 19 ms, overview 47 ms, one note 0.3 ms (medians) |
| Developer install (`node_modules`)                  | 339 MB                                                       |

Details and method: [phase 0](phase-0-baseline-and-scaffold.md).

## Final comparison

Every row was measured on the same machine with the same data for both versions (installer sizes for Windows and Linux
come from CI and the `v1.1.0-rc.1` release). Medians, with the method on each phase's page. **Bold** marks a clear win;
rows where Go is not better say so.

### What you download and run

| Metric                                | Node + Electron  | Go + Wails              | Result                                   | Phase |
| ------------------------------------- | ---------------- | ----------------------- | ---------------------------------------- | ----- |
| macOS installer (universal dmg)       | 231.4 MB         | 15.1 MB                 | **About 15x smaller**                    | 6     |
| Windows setup / portable              | 112.9 / 112.6 MB | 9.1 / 18.1 MB           | **About 12x and 6x smaller**             | 6     |
| Linux deb / rpm                       | 101.9 / 90.3 MB  | 7.5 / 7.8 MB            | **About 13x and 12x smaller**            | 6     |
| Linux AppImage                        | 127.3 MB         | none (6.8 MB `.tar.gz`) | Not offered: WebKitGTK cannot be bundled | 6     |
| Launch to first screen, empty library | 593 ms           | 513 ms                  | Go about 14% faster                      | 5     |
| Launch to first screen, 10k library   | 4,384 ms         | 1,482 ms                | **About 3x faster**                      | 5     |
| Idle memory, empty library            | 510 MB           | 257 MB                  | **About half**                           | 5     |
| Idle memory, 10k library              | 896 MB           | 481 MB                  | **About half**                           | 5     |
| Processes                             | 4                | 4                       | Same                                     | 5     |
| Network ports open                    | 1 (plus a token) | 0                       | **None**                                 | 4, 5  |

### The backend, same library and requests (10k notes, 5k commands)

| Metric                                    | Node                 | Go                        | Result                                    | Phase |
| ----------------------------------------- | -------------------- | ------------------------- | ----------------------------------------- | ----- |
| Open the library (load and index)         | 3,395 ms             | 676 ms                    | **About 5x faster** (one start each)      | 4     |
| Search `nmap` / `-sV` / prefix `kern`     | 8.6 / 15.9 / 12.6 ms | 2.6 / 3.2 / 3.2 ms        | **3.3x to 5x faster**                     | 4     |
| Search an exact name (`cmd:cmd42`)        | 1.2 ms               | 1.7 ms                    | **Node about 1.4x faster**                | 2, 4  |
| Dashboard                                 | 38.4 ms              | 11.6 ms                   | **About 3.3x faster**                     | 4     |
| One note                                  | 0.29 ms              | 0.10 ms (0.01 in the app) | **About 3x faster**                       | 4     |
| List 50 notes / 500 notes                 | 6.3 / 8.7 ms         | 4.0 / 6.9 ms              | Go 1.3x to 1.6x faster                    | 4     |
| List 500 commands                         | 3.2 ms               | 4.5 ms                    | **Node about 1.4x faster**                | 4     |
| Edit one note                             | 8.9 ms               | 5.3 ms                    | Go about 1.7x faster                      | 4     |
| Unlock an encrypted library (scrypt 2^15) | 70 ms                | 58 ms                     | Go about 17% faster; nearly all scrypt    | 4     |
| Peak memory, API benchmark                | 876 MB               | 302 MB                    | **About 2.9x less**                       | 4     |
| Read a Notion export zip                  | 148 ms               | 18.6 ms                   | **About 8x faster**                       | 3     |
| Plan a Notion import (3,600 pages)        | 416 ms               | 329 ms                    | Go about 1.3x faster                      | 3     |
| Commit that import                        | 24.9 s               | 20.9 s                    | Go 16% faster; both slow (write per item) | 3     |
| Re-import, nothing changed                | 841 ms               | 3.9 s                     | **Node about 4.7x faster; unresolved**    | 3     |
| Export the Markdown vault / CSV           | 1,212 / 50.6 ms      | 849 / 27.5 ms             | Go 1.4x to 1.8x faster                    | 3     |
| Export everything as JSON                 | 32.9 ms              | 68 ms                     | **Node about 2x faster**                  | 3     |
| Memory after loading the store            | 189 to 224 MB        | 30 to 34 MB               | **About 6x less**                         | 1     |
| Zip backup of the library                 | 1.9 to 3.2 s         | 1.2 to 2.2 s, once 8.3 s  | Inconclusive                              | 1     |

### Building it

| Metric                                        | Node + Electron                       | Go + Wails                                                | Result                        | Phase |
| --------------------------------------------- | ------------------------------------- | --------------------------------------------------------- | ----------------------------- | ----- |
| `node_modules` for development                | 646 MB                                | 229 MB (plus 72 MB of Go modules and the 32 MB Wails CLI) | **About a third**             | 6, 7  |
| Runtime code (non-blank lines, without tests) | 6,941 (plus 1,702 shared with the UI) | 16,130                                                    | **Go is about twice as long** | 7     |
| Runtime dependencies                          | 64 npm packages plus Electron         | 24 Go modules (6 direct)                                  | Fewer, nothing bundled        | 7     |
| CI test job on Windows                        | 1 min 47 s                            | 1 min 28 s                                                | About the same                | 6     |

### Where Go is not better

- **Exact-name search and listing 500 commands** are about a millisecond slower.
- **Re-importing an unchanged export** was 4.7x slower in the benchmark, traced to the pre-import backup taken after a
  long write burst; not explained.
- **The JSON export** is about 2x slower (Go's standard JSON encoder on a 29 MB document).
- **More code to maintain**: about twice as many lines, because Go does by hand what the Node version took from
  libraries (search, CSV, validation, HTTP).
- **No AppImage** for Linux.
