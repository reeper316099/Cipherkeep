# Moving Cipherkeep to Go + Wails

This folder tracks a staged move from **Node + Electron** to **Go + Wails**. Each phase has its own page with
what changed, how it was tested, and a **measured** comparison with the Node + Electron version.

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
  can appear. The accessibility audit and end-to-end tests have to be rebuilt for it (phase 7).
- **The TypeScript code is the reference.** The study engine, Notion parsing and schemas are ported to Go
  and checked against the existing TypeScript tests and fixtures until the cut-over.

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

| Phase | Page                                                                        | Status  |
| ----- | --------------------------------------------------------------------------- | ------- |
| 0     | [Baseline and scaffold](phase-0-baseline-and-scaffold.md)                   | Done    |
| 1     | [Storage engine (jsonstore in Go)](phase-1-storage-engine.md)               | Done    |
| 2     | [Repositories, search, links, study engine](phase-2-domain-search-study.md) | Done    |
| 3     | [Import and export](phase-3-import-export.md)                               | Planned |
| 4     | [API layer and vault](phase-4-api-and-vault.md)                             | Planned |
| 5     | [Wails shell and UI](phase-5-wails-shell-and-ui.md)                         | Planned |
| 6     | [Packaging and CI](phase-6-packaging-and-ci.md)                             | Planned |
| 7     | [Parity tests and cut-over](phase-7-parity-and-cutover.md)                  | Planned |

The Node + Electron app keeps working, unchanged, until phase 7. The Go code lives in `go/`.

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
