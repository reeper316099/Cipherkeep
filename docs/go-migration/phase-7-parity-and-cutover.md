# Phase 7: parity tests and cut-over

**Status:** planned. Nothing on this page has been measured yet.

## Goal

Prove the Go app does everything the Node app did, then make it the default and retire the Electron code.

## Scope

End-to-end tests and the accessibility audit for the web view, a full parity checklist, performance comparison across all phases, user-facing docs and migration notes, and removing `apps/desktop` and the Node server once Go is the release path.

## Design

- End-to-end tests drive the real app (Playwright over the web view where possible, otherwise a scripted harness) and keep the axe audit of every page in both themes.
- A parity checklist maps every spec feature to a test that passes in the Go app.
- A library written by the Node app is opened by the Go app as the final check (including an encrypted one).

## How it will be tested

- The ported end-to-end flows: first run, starter pack, import, study, restart persistence, encrypted unlock, accessibility.
- Manual pass on all three systems.
- The user's real library opened in a copy first, never in place.

## Compared with Node + Electron

Filled in when the phase is finished, from measurements made the way [phase 0](phase-0-baseline-and-scaffold.md)
describes. If Go is not better on a row, or the difference is within noise, this page will say so.

| Metric                                         | Node + Electron (baseline)                | Go + Wails       | Change |
| ---------------------------------------------- | ----------------------------------------- | ---------------- | ------ |
| All metrics from phases 1 to 6                 | see those pages                           | not measured yet |        |
| Lines of runtime code and runtime dependencies | not counted yet; will be counted for both | not measured yet |        |

## Done when

- [ ] Every item on the parity checklist passes.
- [ ] The final comparison table in this folder is complete, with real numbers and an honest note on every row where Go is not better.
- [ ] The Electron app and Node server are removed, and the README describes the Go app.

## Risks

- Cut-over is the point of no return for the Electron code. It is only done after the checklist passes and a release has been tested by the user.
