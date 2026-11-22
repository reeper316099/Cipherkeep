# Phase 3: import and export

**Status:** planned. Nothing on this page has been measured yet.

## Goal

Port the importers and exporters so any existing import (and undo) still works.

## Scope

Notion export (zip and folder), CSV, Markdown folder, JSON backup; preview, merge strategies, idempotent re-import by reference, undo manifests; Markdown vault, CSV and JSON export; the starter pack; and the optional Notion API import.

## Design

- `archive/zip` and `encoding/csv` from the standard library; `yaml.v3` for frontmatter.
- The Notion planner stays a pure function from files to a plan, as it is today, so it can be compared with the TypeScript plan output file by file.
- The Notion API client keeps its rules: only `api.notion.com`, a token that never touches disk or logs, paced requests, no downloads of Notion-hosted files.
- The starter pack is embedded in the binary with `go:embed`.

## How it will be tested

- The synthetic Notion export must give the exact same counts, warnings and entities as the TypeScript importer. Plans are compared as JSON.
- Round trips: export then import, backup then restore.
- The Notion API tests use a fake server, as the TypeScript tests do, and assert nothing else is contacted.
- Run the importer against the user's private export locally only, and compare counts. Nothing from it goes into the repository.

## Compared with Node + Electron

Filled in when the phase is finished, from measurements made the way [phase 0](phase-0-baseline-and-scaffold.md)
describes. If Go is not better on a row, or the difference is within noise, this page will say so.

| Metric                               | Node + Electron (baseline)                         | Go + Wails       | Change |
| ------------------------------------ | -------------------------------------------------- | ---------------- | ------ |
| Import the synthetic export (commit) | about 0.3 to 1.4 s depending on the test           | not measured yet |        |
| Import a 10k-note Markdown folder    | not measured on Node yet; will be measured in both | not measured yet |        |
| Starter pack load                    | about 1 s                                          | not measured yet |        |

## Done when

- [ ] Plan output matches the TypeScript importer for every fixture.
- [ ] Undo restores exactly what an import changed.
- [ ] The Notion API import passes against the fake server, with no other host contacted.

## Risks

- This is the largest port. The Notion parsing has many special cases; the fixtures are the safety net.
