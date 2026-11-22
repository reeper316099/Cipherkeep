# Phase 0: baseline and scaffold

## Goal

Measure the Node + Electron app properly, so later phases are compared with real numbers, and create the empty Go
project that the other phases fill in.

## What was done

- Measured installer sizes, memory, start time and the 10k-note server benchmark (below).
- Created the Go module in `go/` (`github.com/reeper316099/Cipherkeep/go`). Toolchain on the reference machine:
  Go 1.27.4, Wails v2.17.1.
- Fixed one bug the benchmark exposed in the Node version: a file disappearing while a backup was being zipped
  raised an uncaught error and could crash the process. It now fails that one backup cleanly (regression test
  added). This matters for the comparison because the Go port must handle the same case.

## Decisions

| Decision            | Choice                                                             | Why                                                                                                             |
| ------------------- | ------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------- |
| Wails version       | v2 (stable)                                                        | v3 is still alpha. Revisit once it is stable.                                                                   |
| Frontend            | Reuse `apps/web` (React + Vite)                                    | Rewriting the UI is the largest risk and gives nothing the user sees.                                           |
| Backend to frontend | HTTP-shaped API served **in process**                              | Wails can answer `/api/...` from Go without opening a network port, so the UI code barely changes. See phase 4. |
| Data format         | Unchanged                                                          | Lets the two versions open each other's libraries, and gives a clean parity test.                               |
| Source of truth     | The TypeScript code until phase 7                                  | The Go port is tested against its fixtures and test cases.                                                      |
| Go dependencies     | Standard library first; `fsnotify`, `yaml.v3`, `x/crypto` (scrypt) | Small, widely used, no cgo required.                                                                            |

## Baseline: Node + Electron

Measured 2026-11-21 on the reference machine. Memory is summed resident memory of the whole process tree
(`ps`), which counts shared pages more than once; the same method is used for Wails.

### Size

| Artifact                       | Size     |
| ------------------------------ | -------- |
| macOS universal dmg            | 231.4 MB |
| Windows setup                  | 112.9 MB |
| Windows portable               | 112.6 MB |
| Linux AppImage                 | 127.3 MB |
| Linux deb                      | 101.1 MB |
| Linux rpm                      | 90.3 MB  |
| `node_modules` for development | 339 MB   |

Source: the sizes of the assets on the v1.0.3 GitHub release.

### Running app (empty library, macOS arm64)

| Metric                  | Run 2  | Run 3  |
| ----------------------- | ------ | ------ |
| Launch to first screen  | 668 ms | 673 ms |
| Memory 10 s after start | 525 MB | 518 MB |
| Processes               | 4      | 4      |

Run 1 is discarded because it included downloading the Electron binary. Method: a Playwright script launches the
built app with a temporary data folder, waits for the first-run welcome text, sleeps 10 s, then sums memory.

### Server with 10,000 notes and 5,000 commands (`npm run bench`)

| Measurement                          | Result                                   |
| ------------------------------------ | ---------------------------------------- |
| Start (load, index, watch)           | 5,450 ms, 281 MB heap                    |
| `GET /notes?limit=50`                | 9.0 ms median                            |
| `GET /notes?limit=500&omit=markdown` | 8.8 ms median (144 KB)                   |
| `GET /commands?limit=500`            | 4.1 ms median                            |
| search `nmap` (all types)            | 9.8 ms median                            |
| search `-sV`                         | 18.8 ms median                           |
| search `cmd:cmd42`                   | 1.2 ms median                            |
| search prefix `kern`, notes only     | 15.0 ms median                           |
| `GET /graph`                         | 8.7 ms median (1.5 MB)                   |
| `GET /stats/overview`                | 47.2 ms median                           |
| `GET /notes/:id`                     | 0.3 ms median                            |
| `PATCH` one note                     | 76.5 ms (18 ms on an earlier run; noisy) |

The benchmark was run twice. The first run, made while other apps were busy, gave a slower `-sV` search (21.9 ms)
and a 5.8 s start. Differences of that size are noise, so Go results will be compared with ranges, not single runs.

## Go + Wails result for this phase

Nothing to compare yet: this phase only creates the empty module. Not measured yet.

## Done when

- [x] Baseline numbers recorded with their method.
- [x] `go/` module exists.
- [x] A `go test ./...` job and a benchmark harness for the same data set exist (finished in
      [phase 1](phase-1-storage-engine.md), because they need the storage engine).
