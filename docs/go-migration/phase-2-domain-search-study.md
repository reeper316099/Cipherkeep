# Phase 2: repositories, search, links and the study engine

**Status:** done. 101 Go tests pass with the race detector on macOS (CI runs them on macOS, Windows and Linux). About 6,000
lines of Go and 2,350 lines of tests were added in this phase.

## Goal

Port the data model and everything that computes over it: repositories, search, the link graph, the study engine and the
dashboard numbers.

## What was built

All of it lives under `go/internal`:

| Package   | What it does                                                                                                                                                                        |
| --------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `domain`  | Entity model and validation, slugs and title matching, wiki links, questions, settings, taxonomy, skills, templates                                                                 |
| `repo`    | One repository per collection: JSON shards (by category, platform or month) and Markdown notes, including encrypted notes at `_enc/<id>.md`; listing, filtering, sorting and paging |
| `search`  | Full-text index: same tokenizer, BM25 scoring, field boosts, prefix and typo matching, and `cmd:` `tag:` `cat:` `status:` `type:` filters                                           |
| `links`   | Relations, typed links and `[[wiki links]]`; backlinks, outgoing links and the graph                                                                                                |
| `study`   | Spaced repetition (SM-2 variant), card building and typed-answer checking, skill map, threat level, streaks, recommendations                                                        |
| `library` | Opens a whole library and wires the above together; activity log, study service, dashboard                                                                                          |

## Design notes

- **Entities stay JSON objects, not fixed Go structs.** The TypeScript code treats entities as validated objects: patches
  merge fields, lists sort by any field, search reads fields by name. Keeping that shape in Go reproduces the behaviour
  exactly and keeps the shard files identical. Validation follows the same rules and defaults as the Zod schemas
  (including nested defaults, nullable fields, and "patch applies no defaults").
- **Search is my own engine**, because MiniSearch has no Go equivalent. It reproduces MiniSearch's scoring (BM25 with the same
  constants, the same field boosts, prefix and typo weights, AND across terms) so results rank the same way. Words are
  tokenized in parallel and merged in order.
- **Order is deterministic.** Shard files are read in name order and notes in path order. The TypeScript version used
  the operating system's directory order and the finish order of a parallel file scan, so lists, recommendations and tied search
  results could differ between machines (fixed there too, below).
- **Timestamps in note frontmatter stay strings,** as the JavaScript YAML reader does, so a note written by either app
  reads the same in both. Frontmatter key order and quoting can differ between the two writers; the files are equivalent,
  not byte-identical.

## How it was tested

- **Ported tests.** The TypeScript cases for schemas, study engine, search, links, the study service and the dashboard were
  ported case by case, plus new tests for repositories, encrypted notes, hand-edit reload and the tokenizer.
- **Parity with the TypeScript, on one shared library.** `fixtures/compat/parity` holds a realistic library (the starter pack
  plus labs, logs, notes, an archived item and a fixed review history, 85 entities) and `expected.json`, which records what the
  TypeScript code returns for it. The Go tests open the same library and compare: all 28 search queries (scores and order),
  the entity listings, the graph, backlinks and outgoing links, the study queue, stats and questions, the dashboard in two
  time zones, and SM-2 sequences. They match.
- **The parity tests catch mistakes.** Changing a search constant, adding one day to an interval, or altering the recency
  decay each makes them fail.
- **A drift guard on the Node side.** A TypeScript test recomputes `expected.json` and fails if Node's behaviour changes
  without the fixtures being regenerated, so the two versions cannot quietly drift apart.
- **The new tokenizer** was checked against the earlier regexp version on 30,000 random strings full of separators, dashes
  and Unicode.

## Found along the way

Bugs in the **Node** version that the port exposed. They are fixed there too, with tests:

- **Unanswerable fill-in-the-blank cards.** A signed bit shift made the card seed negative for about half of all ids. For a
  command example with several flags, about 37% of commands got a fill card with no blank and no accepted answer.
- **Order depended on the machine.** Shard files were read in directory order and notes in the order their files finished
  loading, so the order of lists, recommendations and tied search results could change between runs and between operating
  systems. Both are now sorted.

## Compared with Node + Electron

Measured with `scripts/bench-lib.ts` and `go/cmd/bench-lib` on the same on-disk library (10,000 notes, 5,000 commands,
200 concepts, 100 labs, 150 log entries), in process, so HTTP is not included (that is phase 4). Each figure is the median of
three rounds with the range in brackets. The machine was in normal use.

Three measurement passes were made, and the table shows each:

1. **Node**, first pass.
2. **Go, first pass**, straight after the port.
3. **Go, after fixes.** The first Go pass was slower than Node in several places, so those were profiled and fixed.

| Metric                                        | Node                | Go, first pass      | Go, after fixes     | Result                                                                               |
| --------------------------------------------- | ------------------- | ------------------- | ------------------- | ------------------------------------------------------------------------------------ |
| Memory after loading and querying (RSS)       | 692 MB (561 to 695) | 296 MB (294 to 296) | 299 MB (297 to 312) | **Go uses about 2.3x less.** Held in both Go passes.                                 |
| Text searches (`nmap`, `-sV`, prefix)         | 7.3 / 13.3 / 8.1 ms | 2.7 / 3.4 / 3.3 ms  | 2.8 / 3.5 / 3.3 ms  | **Go is 2.4 to 3.9x faster.** Held in both Go passes.                                |
| Typo search (`enumarate`)                     | 9.3 ms              | 8.7 ms              | 8.0 ms              | Tie.                                                                                 |
| Exact-name search (`cmd:cmd42`)               | 0.4 ms              | 3.4 ms              | 1.8 ms              | **Node is faster** (about 4x). A trie finds an exact word faster than my index does. |
| Dashboard overview                            | 29.7 ms (24 to 35)  | 40.9 ms (40 to 42)  | 12.7 ms (12 to 13)  | **Go is about 2.3x faster after the fixes.** First pass was slower.                  |
| List 500 notes sorted by title                | 5.1 ms              | 36.6 ms             | 6.3 ms              | Roughly a tie; Node slightly ahead. First pass was far slower.                       |
| Edit one note (write to disk)                 | 5.9 ms              | 3.9 ms              | 4.0 ms              | Go about 1.5x faster.                                                                |
| Edit one command (writes a 625-command shard) | 19.9 ms             | 23.8 ms             | 14.0 ms             | **Go about 1.4x faster after the fixes.** First pass was slower.                     |
| Graph (as JSON)                               | 0.4 ms              | 0.5 ms              | 0.5 ms              | Tie (both under a millisecond).                                                      |
| Read one note                                 | under 0.01 ms       | under 0.01 ms       | under 0.01 ms       | Tie.                                                                                 |
| Open the library (load, index)                | 3.2 s (3.1 to 3.4)  | 2.9 s (2.9 to 5.8)  | 4.8 s (4.1 to 6.0)  | **Inconclusive.** See below.                                                         |

### What was fixed between the Go passes

- Sorting by title compared two strings through the collation library on every step; it now compares cached sort keys.
- Typo matching scanned the whole word list; it now looks only at words of a plausible length.
- JSON files were encoded, decoded and written again; they are now written straight from the Go value (identical output,
  checked by a test), and a shard is no longer deep-copied on every edit.
- Stats built a study card for every command just to count what is due; they now count without building or sorting cards.
  The skill map is scored in a single pass, and the command tokenizer no longer uses regular expressions.

### About "open the library"

Opening is the number I cannot yet give you. The first Go pass looked level with Node, but one of its three rounds was
twice as slow as the others; the second pass was slower again (4.8 s), and the machine was busy in both. Two things are known:

- A separate, isolated measurement showed that **building the search index was about 2.1 s of the roughly 2.9 s open
  time**, almost all of it splitting text into terms. After the tokenizer was rewritten and run in parallel, that same
  build takes **0.10 s** (a micro-benchmark on 15,000 synthetic documents, not the full library).
- The full library has **not been re-measured end to end since that change**, as asked, so no open-time claim is made
  here. It will be measured again as part of the whole-app comparison in phase 5.

### What this phase shows

Go uses far less memory, is faster on text search and the dashboard, and edits shards faster. Node is still faster at
exact-name lookups and about equal at sorted listings and the graph. Open time is unresolved until it is measured again.

## Done when

- [x] Every ported test case passes, plus the shared fixtures run in both languages.
- [x] Search results match the TypeScript version on the benchmark queries (scores and order).
- [x] Overview and graph numbers match on the shared library, in two time zones.
- [x] Measurements recorded for both versions, including where Go is slower and where a result is inconclusive.
