# Phase 3: import and export

**Status:** done. 48 new Go test functions pass with the race detector on macOS (CI runs them on macOS, Windows and Linux),
and vet is clean for all three systems. About 5,000 lines of Go and 2,000 lines of tests were added.

## Goal

Port the importers and exporters so any existing import (and undo) still works.

## What was built

All of it lives under `go/internal`:

| Package     | What it does                                                                                                                                                                                   |
| ----------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `notion`    | Reads what a Notion export looks like: file names and ids, Markdown pages with property blocks, tables, relation cells, dates and durations; maps command, concept and log pages to fields     |
| `csvx`      | CSV reader and writer that behave like PapaParse (guessed delimiter, quoted newlines, duplicate headers, short rows)                                                                           |
| `importer`  | Virtual file system (zip, folder, upload) with size and zip-slip limits; the Notion, CSV, Markdown-folder and JSON-backup planners; commit with merge strategies; undo; history; preview store |
| `exporter`  | Markdown vault (zip), CSV per collection, CSV template, JSON backup, with private lab writeups left out unless asked                                                                           |
| `starter`   | The starter pack, embedded in the binary, as an ordinary import plan                                                                                                                           |
| `notionapi` | The optional Notion API import: polite client, block and property conversion, background job                                                                                                   |
| `sanitize`  | Redacts flags, addresses, credentials and keys from a writeup before sharing                                                                                                                   |

## Design notes

- **Planning is still a pure function** from files to a plan. Nothing is written until commit, and commit takes a
  pre-import backup first, so "preview, commit, undo" works as before. The plan is plain data and is compared with the
  TypeScript plan as JSON.
- **A CSV reader of my own, not `encoding/csv`.** The standard one is strict about quotes and field counts and cannot
  guess a delimiter, and Notion and spreadsheet exports need all three. The new one follows PapaParse's rules and has its
  own tests.
- **Some Notion patterns need lookahead or backreferences**, which Go's standard regexp engine does not have. Those few
  (fenced blocks, `*emphasis*` in table cells) use the `regexp2` package; everything else uses the standard engine, which
  cannot backtrack catastrophically.
- **JavaScript whitespace and sorting are reproduced on purpose.** JavaScript's `\s`, `trim()` and default sort treat
  non-breaking spaces and emoji differently from Go's, and the importer's output depends on all three. The parity tests
  found two places where this mattered.
- **The starter pack is embedded** (`go:embed`) from a copy in `go/internal/starter/data`. A test fails if the copy ever
  differs from `starter-pack/`, which the Node app still bundles.
- **The Notion API import keeps its rules.** Only `api.notion.com` is ever contacted (the base URL is fixed in code), the
  token lives only in the running job and in request headers, errors never contain it, requests are paced at about three
  a second, and Notion-hosted files are not downloaded.

### Where Go behaves differently on purpose

These are the places the Go code is not a line-for-line copy. Each is a small improvement or a forced difference:

- **Zip size limits count the bytes actually read**, not the size the archive claims, so a lying archive cannot slip past.
- **Custom sanitiser patterns use Go's regexp engine.** A pattern that needs lookahead is reported as invalid instead of being
  applied. In exchange a hostile pattern cannot hang the app.
- **A non-numeric value in a numeric CSV column** (`abc` in Points) leaves the field unset and the row still imports.
  The TypeScript version produced NaN, which then failed validation for the whole import.
- **Notion API:** if Notion sends `429` without a `Retry-After` header, Go waits 2 seconds (TypeScript waited none, because
  `Number(null)` is 0). Database columns are written title first, then in alphabetical order, because Go does not keep
  the order of JSON object keys.
- **Markdown vault frontmatter** is written by a different YAML library, so quoting and line wrapping can differ from the
  Node version. The files are equivalent and re-import identically (checked field by field), not byte-identical.

## How it was tested

- **Ported tests.** The cases from `notion.test.ts`, `import.test.ts`, `starter.test.ts`, `notion-api.test.ts` and the
  sanitiser tests were ported one by one: exact counts on the synthetic Notion export, field mapping, idempotent re-import
  that never overwrites an edit, overwrite then undo, undoing a whole import, reclassifying loose pages, zip uploads
  with Unicode names and `_all.csv` duplicates, zip-slip, the three exports and their round trips, and the fake Notion
  server (only `api.notion.com`, the pinned version, the token as a bearer header, back-off on 429, and the token found
  nowhere on disk).
- **Parity with the TypeScript, on shared inputs.** `fixtures/compat/importers/expected.json` records what the TypeScript
  code returns for: about 200 hand-picked parser inputs (durations, dates, relation cells, pages, tables, callouts,
  placeholders, risk), the planners (the synthetic Notion export, the starter pack, three CSV files, a Markdown folder, a
  JSON backup), every export of a shared library, and the sanitiser. The Go tests compute the same things and compare
  them as JSON. They match.
- **The parity tests catch mistakes.** Shortening the attachment hash by one character fails them.
- **A drift guard on the Node side.** A TypeScript test recomputes `expected.json` and fails if Node's behaviour changes
  without the fixtures being regenerated.
- **Local only:** the importer was run against the real private export (counts, field mapping and a second, empty
  re-import). Only counts are asserted and nothing from it is in the repository; the test skips itself when the file is
  missing.

## Found along the way

Bugs in the **Node** version that the port exposed. They are fixed there too, with tests:

- **A stray carriage return in the CSV template.** The template ended in `\r\n\n` instead of one newline.
- **Half an emoji in a snippet description.** Cutting a description at 300 characters could split an emoji in two and
  write an invalid character into the plan.

And two things in the Go port that the parity tests caught before they shipped: a non-breaking space between a number and
its unit ("1 hr") was not recognised, and a JSON cell in a CSV export escaped `<` and `>` where JavaScript does not.

## Compared with Node + Electron

Measured with `scripts/bench-import.ts` and `go/cmd/bench-import` on the same on-disk data, in process (so HTTP is not
included; that is phase 4). The data is a generated Notion export of 3,600 pages (2,000 commands, 100 concepts and 1,500
loose notes, 11 MB as a zip) for the import rows, and the phase 2 library (10,000 notes, 5,000 commands, 200 concepts, 100
labs, 150 log entries) for the export rows. Each figure is the median of three rounds in one run, with the range in
brackets. The machine was in normal use.

Two Go passes are shown. The first was slower than Node on two rows, so those were profiled and fixed, and Go was
measured once more (Node was not run again; its code did not change).

| Metric                                      | Node                      | Go, first pass        | Go, after fixes       | Result                                                                      |
| ------------------------------------------- | ------------------------- | --------------------- | --------------------- | --------------------------------------------------------------------------- |
| Read the zip into memory                    | 148 ms (101 to 160)       | 18.5 ms (18.5 to 20)  | 18.6 ms (18 to 28)    | **Go about 8x faster.** Held in both passes.                                |
| Plan the Notion export                      | 416 ms (398 to 416)       | 1,456 ms              | 329 ms (325 to 345)   | **Go about 1.3x faster after the fixes.** First pass was 3.5x slower.       |
| Commit the plan (create 3,600 items)        | 24.9 s (24.0 to 25.0)     | 20.8 s (20.7 to 20.9) | 20.9 s (20.9 to 21.3) | Go about 16% faster. Both are slow, see below.                              |
| Re-import the same export (nothing changes) | 841 ms (832 to 854)       | 4.9 s (4.5 to 5.3)    | 3.9 s (3.6 to 4.5)    | **Node is faster, about 4.7x. Unresolved.** See below.                      |
| Export the Markdown vault (15k+ files)      | 1,212 ms (1,203 to 1,229) | 850 ms (849 to 851)   | 849 ms (844 to 851)   | **Go about 1.4x faster.** Held in both passes.                              |
| Export commands as CSV                      | 50.6 ms                   | 28.8 ms               | 27.5 ms               | **Go about 1.8x faster.**                                                   |
| Export everything as JSON (29 MB)           | 32.9 ms (32 to 34)        | 63.3 ms (63 to 71)    | 68.0 ms (66 to 68)    | **Node is faster, about 2x.** Go's JSON encoder is slower on big documents. |
| Peak memory (RSS)                           | 1,540 MB                  | 537 MB                | 554 MB                | **Go uses about 2.8x less.** Held in both passes.                           |

### What was fixed between the Go passes

- Matching titles built a new string replacer on every call, and the planner re-normalised every page title for every CSV
  row (a quadratic loop). Titles are now normalised in one pass with no allocation, and pages are looked up in a map. This
  alone took planning from 1,456 ms to about 330 ms.

### The two rows where Go loses

- **Re-import.** The CPU profile of the second commit, run on its own, shows it takes about 0.6 s (planning about 0.3 s,
  the rest is the pre-import backup). In the benchmark the same step takes 3.6 to 5 s. The difference is the backup that
  commit takes first: it runs right after about 20 s of writes, and zipping 5,000 small files then is slow in Go in a way
  I have not pinned down. In phase 1 the same kind of backup was faster in Go than in Node, so this row is one to look at
  again, in phase 7, with a quieter machine. It is reported as measured, not explained.
- **JSON export.** Go's standard encoder is the cost. Writing the same document with the storage engine's own writer
  would not be faster (it sorts keys), so this is left as is: 68 ms for a 29 MB file.

### About the commit row

Both versions take about 20 seconds to import 3,600 items, and Go's small lead there is not the story. Every item is
created with a separate write that rewrites its whole shard file, so a big import rewrites the same files thousands of
times. Neither version batches the writes. Doing so (one write per shard) should cut a large import from seconds to well
under one second in both languages. It is a worthwhile follow-up but changes the repository interface, so it is not part
of this port.

### Not measured

- **Attachments.** The generated export links no attachments (a path mistake in my generator), so copying attachments is
  covered by the fixture tests only, not by this benchmark.
- **The starter pack load** (the old table promised it). It is 62 items, so it takes a fraction of a second in both
  versions; it was not benchmarked on its own.
- **Importing a 10,000-note Markdown folder.** Not measured. The planning and commit costs above are the same code paths.

### What this phase shows

Go is faster at reading archives, planning an import and building the Markdown and CSV exports, and uses far less memory.
Node is faster at the JSON export and, in this run, at re-importing, for a reason I have not found yet. Importing many
items is slow in both and is limited by how writes are done, not by the language.

## Done when

- [x] Plan output matches the TypeScript importer for every fixture (parity tests, plus the ported cases).
- [x] Undo restores exactly what an import changed (idempotence, overwrite-then-undo and full undo are tested).
- [x] The Notion API import passes against the fake server, with no other host contacted.
- [x] Measurements recorded for both versions, including where Go is slower and where a result is unresolved.

## Risks that remain

- The Markdown vault frontmatter is written by a different YAML library; the round trip is tested, but a hand-written
  Obsidian plugin that expects the Node layout byte for byte would notice the difference.
- The backup slowness above could also affect "launch" and "pre-undo" backups on very large libraries.
