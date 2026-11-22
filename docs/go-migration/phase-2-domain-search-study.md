# Phase 2: repositories, search, links and the study engine

**Status:** planned. Nothing on this page has been measured yet.

## Goal

Port the data model and everything that computes over it: repositories, search, the link graph, the study engine and the dashboard numbers.

## Scope

Entity types and validation (the Zod schemas), the JSON and Markdown repositories (including encrypted notes at `_enc/<id>.md`), taxonomy and templates, activity log, search with the dash-preserving tokenizer and the `cmd:`, `tag:`, `cat:`, `status:` and `type:` operators, backlinks and graph, spaced repetition (the SM-2 variant), card types and tolerant answer checking, skill-map scoring, threat level, streak and heatmap, and recommendations.

## Design

- Validation is written by hand as Go types plus checks that return the same field-level errors as the API does today.
- Search is a small inverted index written for this app. MiniSearch is not available in Go, and a general search engine such as Bleve is far larger than the need. The tokenizer and ranking are ported so results match.
- Study and dashboard logic are pure functions, so they can be tested against the TypeScript outputs.

## How it will be tested

- Shared fixtures: the TypeScript study and Notion tests are turned into JSON case files that both languages run.
- A search parity test runs the same queries on the 10k-note data set in both versions and compares result order.
- Property tests for the SM-2 interval limits and answer checking.

## Compared with Node + Electron

Filled in when the phase is finished, from measurements made the way [phase 0](phase-0-baseline-and-scaffold.md)
describes. If Go is not better on a row, or the difference is within noise, this page will say so.

| Metric                                        | Node + Electron (baseline) | Go + Wails       | Change |
| --------------------------------------------- | -------------------------- | ---------------- | ------ |
| search `nmap`, `-sV`, `cmd:`, prefix (median) | 1.2 to 18.8 ms             | not measured yet |        |
| `GET /stats/overview`                         | 47.2 ms median             | not measured yet |        |
| `GET /graph`                                  | 8.7 ms median              | not measured yet |        |
| Index memory with 10k notes + 5k commands     | part of the 281 MB heap    | not measured yet |        |

## Done when

- [ ] Every ported test case passes, plus the shared fixtures run in both languages.
- [ ] Search results match the TypeScript version on the benchmark queries.
- [ ] Overview and graph numbers match on the synthetic library.

## Risks

- Ranking differences would be visible to the user. Result order is compared, not just result sets.
- The study engine is subtle (intervals, lapses, caps). It is ported test by test.
