# Phase 6: packaging and CI

**Status:** planned. Nothing on this page has been measured yet.

## Goal

Build installers for all three systems with Wails and run the whole test suite in CI.

## Scope

`wails build` for macOS (universal), Windows (NSIS and portable) and Linux (AppImage, deb, rpm), the release workflow, the CI matrix, and the notes on unsigned builds and how to add signing later.

## Design

- GitHub Actions matrix as today, with Go and Node set up (Node for the frontend build only).
- Linux needs the WebKitGTK development packages; Windows relies on WebView2, which is present on current Windows.
- The release job attaches the new installers and records their sizes in the release notes.

## How it will be tested

- Build and smoke-test each installer in CI.
- Install-size and download-size are measured from the produced files, not estimated.

## Compared with Node + Electron

Filled in when the phase is finished, from measurements made the way [phase 0](phase-0-baseline-and-scaffold.md)
describes. If Go is not better on a row, or the difference is within noise, this page will say so.

| Metric                               | Node + Electron (baseline)        | Go + Wails       | Change |
| ------------------------------------ | --------------------------------- | ---------------- | ------ |
| macOS installer                      | 231.4 MB                          | not measured yet |        |
| Windows installer (setup / portable) | 112.9 MB / 112.6 MB               | not measured yet |        |
| Linux AppImage / deb / rpm           | 127.3 / 101.1 / 90.3 MB           | not measured yet |        |
| CI time, full verify job             | about 1.5 to 2 minutes on Windows | not measured yet |        |
| Developer install                    | 339 MB `node_modules`             | not measured yet |        |

## Done when

- [ ] Installers build for all three systems from a tag.
- [ ] Sizes are recorded here next to the Node + Electron ones.
- [ ] Unsigned-build notes are updated.

## Risks

- Wails apps need the WebView2 runtime on Windows; very old systems may lack it. The installer can bootstrap it.
