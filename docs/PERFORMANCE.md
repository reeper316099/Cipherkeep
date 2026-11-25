# Performance

Target: stay responsive with 10,000 notes and 5,000 commands on a normal laptop.

The measurements live with the move to Go + Wails, where every figure was taken for both the old Node + Electron
version and the Go version on the same machine and data: [docs/go-migration](go-migration/README.md). Nothing leaves
your machine while they run.

## Measured (10k notes + 5k commands, Apple-silicon laptop)

| Measurement                              | Go + Wails    | Node + Electron (before) |
| ---------------------------------------- | ------------- | ------------------------ |
| Launch to first screen with that library | 1.5 s         | 4.4 s                    |
| Idle memory, whole app                   | 481 MB        | 896 MB                   |
| Search (`nmap`, `-sV`, prefix)           | 2.6 to 3.2 ms | 8.6 to 15.9 ms           |
| Dashboard (`GET /stats/overview`)        | 11.6 ms       | 38.4 ms                  |
| One note (`GET /notes/:id`, in the app)  | 0.01 ms       | 0.29 ms                  |
| Edit one note (`PATCH`)                  | 5.3 ms        | 8.9 ms                   |

API figures are over loopback HTTP for both, except where marked; in the app the Go API is called in process.

## How it stays fast

- **Lean list responses**: list endpoints accept `omit=markdown,sections,...`. The UI leaves note bodies out of lists
  and fetches a note when you open it.
- **A search index of its own**: BM25 scoring with prefix and typo matching, built in parallel when the library opens.
- **Background work after the first screen**: the launch backup runs 3 s after start and streams to a temp file; file
  watching starts once the library is shown.

## Known limits

- Start-up is dominated by reading and indexing every note once. It scales roughly linearly.
- Everything is held in memory.
- Listing 500 commands and exact-name lookups (`cmd:name`) are about a millisecond slower than in the Node version.
- Importing thousands of items at once takes seconds, because each item rewrites its shard file.
