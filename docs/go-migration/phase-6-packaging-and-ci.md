# Phase 6: packaging and CI

**Status:** built and measured on macOS; set up for Windows and Linux in CI, where it has not run yet. The Windows and
Linux rows below stay empty until the first CI run produces the files.

## Goal

Build installers for all three systems with Wails and run the whole test suite in CI.

## What was built

| Path                               | What it does                                                                                                                                                    |
| ---------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `scripts/package-app.mjs`          | Builds the app and its installers for the current system into `release-go/` and prints their sizes                                                              |
| `scripts/smoke-app.mjs`            | Starts the built app with its window hidden and checks the UI, the in-process API and live updates work, then quits                                             |
| `go/app/build/linux/nfpm.yaml`     | The `.deb` and `.rpm` (with the GTK and WebKitGTK 4.1 runtime packages as dependencies) and a desktop entry                                                     |
| `.github/workflows/ci.yml` (`app`) | On every push, on all three systems: build the app and installers, run the smoke test, keep the files for 7 days                                                |
| `.github/workflows/release.yml`    | A `v*` tag also builds the Go installers (`Cipherkeep-Go-…`) next to the Electron ones, smoke-tests them, and lists every installer's size in the draft release |

| System  | Installers                                                   |
| ------- | ------------------------------------------------------------ |
| macOS   | Universal `.dmg` (arm64 and x86_64), made with `hdiutil`     |
| Windows | NSIS setup (Wails' installer template) and a portable `.exe` |
| Linux   | `.deb`, `.rpm` (nfpm) and a `.tar.gz` of the program         |

### Different from the Electron packaging

- **No AppImage.** An AppImage carries its libraries with it, and WebKitGTK cannot be bundled reliably; Wails does not
  offer it either. Linux gets `.deb`, `.rpm` and a plain archive, which use the system's WebKitGTK.
- **Windows needs the WebView2 runtime.** It is part of Windows 11 and current Windows 10. If it is missing, Wails' setup
  installer downloads Microsoft's runtime installer: the one moment the setup touches the network, and only on systems
  without WebView2. The portable `.exe` does not do this and will not start without WebView2.
- **The macOS signing step** of the Wails CLI sometimes fails on a universal build ("replacing existing signature") after
  the app is fully built. The script then signs it ad hoc itself and verifies the signature, so a broken build still
  fails.

## How it was tested

- **macOS, locally:** the dmg was built, mounted, and the app inside it checked: both architectures present, the right
  version in `Info.plist`, and the smoke test passed on it (first screen data after 1.6 s from a mounted disk image, live
  update received, library lock released on quit).
- **Windows and Linux:** the CI `app` job builds and smoke-tests them on every push. It has **not run yet**; the first push
  of this phase is its first test. The Linux package config could not be checked locally (building Linux packages needs
  the Linux app, which needs a Linux machine).

## Compared with Node + Electron

macOS figures measured on the reference machine; Electron figures are from phase 0 (the v1.0.3 release assets).

| Metric                               | Node + Electron                            | Go + Wails                                             | Result                                                                                                            |
| ------------------------------------ | ------------------------------------------ | ------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------- |
| macOS installer (universal dmg)      | 231.4 MB                                   | **15.1 MB**                                            | **About 15x smaller.**                                                                                            |
| macOS app on disk (universal)        | not measured                               | 32 MB                                                  |                                                                                                                   |
| Windows installer (setup / portable) | 112.9 MB / 112.6 MB                        | not measured yet                                       | From the first CI run.                                                                                            |
| Linux AppImage / deb / rpm           | 127.3 / 101.1 / 90.3 MB                    | no AppImage / not measured yet                         | No AppImage (see above). deb and rpm from the first CI run.                                                       |
| CI time                              | about 1.5 to 2 min (verify job on Windows) | not measured yet                                       | From the first CI run.                                                                                            |
| Go modules a developer downloads     | none                                       | 14 modules, 72 MB unpacked, plus the Wails CLI (32 MB) | Only what the build compiles on the three systems; the whole module graph, test-only modules included, is 235 MB. |
| `node_modules`                       | 646 MB today                               | the same, for now                                      | Still needed to build the UI. Electron alone is 312 MB of it and goes away at the cut-over (phase 7).             |

The phase 0 page recorded `node_modules` as 339 MB. It is 646 MB today with the same lock file plus the test tools added
since; measured now, not estimated.

## Done when

- [ ] Installers build for all three systems from a tag. **Set up; macOS verified locally, Windows and Linux on the
      first CI run.**
- [ ] Sizes are recorded here next to the Node + Electron ones. **macOS done; the rest after that run.**
- [x] Unsigned-build notes are updated (`docs/PACKAGING.md`).

## Risks

- The Windows and Linux builds and the hidden-window smoke test have never run. WebView2 and WebKitGTK may behave
  differently with a hidden window; if the smoke test fails there, the first thing to try is a visible window under a
  virtual display.
