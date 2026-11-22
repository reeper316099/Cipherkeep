# Phase 5: Wails shell and UI

**Status:** planned. Nothing on this page has been measured yet.

## Goal

Wrap the Go backend and the existing React UI in a Wails window.

## Scope

The window and its saved size, native menu, data-folder choice and move, open-in-browser links, single-instance behaviour, the no-network policy for the web view, and the small UI changes needed to talk to the in-process API.

## Design

- `wails.json` points at `apps/web` for the frontend build.
- Content-Security-Policy stays strict. Outbound requests from the web view are not needed and are not allowed.
- The preload bridge (`window.cipherkeep`) is replaced by Wails bindings with the same shape for the few things the UI uses (folder pickers, open-external, data dir).
- Dev flow: `wails dev` with hot reload.

## How it will be tested

- Manual and scripted checks on all three systems.
- A headless smoke test of the backend without opening any window, so it can run in the background.
- A scripted measurement of start time and idle memory, run only when nobody is using the machine because it opens a window.

## Compared with Node + Electron

Filled in when the phase is finished, from measurements made the way [phase 0](phase-0-baseline-and-scaffold.md)
describes. If Go is not better on a row, or the difference is within noise, this page will say so.

| Metric                                | Node + Electron (baseline)                         | Go + Wails       | Change |
| ------------------------------------- | -------------------------------------------------- | ---------------- | ------ |
| Launch to first screen, empty library | about 0.67 s                                       | not measured yet |        |
| Idle memory, empty library            | about 520 MB, 4 processes                          | not measured yet |        |
| Idle memory, 10k notes                | not measured on Node yet; will be measured in both | not measured yet |        |
| Processes                             | 4                                                  | not measured yet |        |

## Done when

- [ ] The app runs on macOS, Windows and Linux with the real UI.
- [ ] Start time and idle memory are recorded for both versions.
- [ ] The web view cannot reach the network.

## Risks

- The system web view differs per OS (WebKit, WebView2, WebKitGTK). CSS and a few APIs can behave differently from Chromium; the UI is checked on each.
- Native menus and window state need per-OS work.
