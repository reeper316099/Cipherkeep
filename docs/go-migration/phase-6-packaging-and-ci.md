# Phase 6: packaging and CI

**Status:** done. All three systems build the app and its installers in CI, and the app passes the smoke test on all
three (the real UI loads, talks to the in-process API, and receives a live update after a hand edit). The last Windows
run failed only while deleting its temporary folder afterwards; that clean-up is fixed here.

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
- **Linux, in CI:** the first run built the app and its packages and passed the smoke test under a virtual display:
  the real UI ran in WebKitGTK, its first screen data arrived after 2.0 s, and a hand edit reached the window.
- **The first CI run also failed in three places**, all fixed here:
  - **Windows build:** the script ran the Wails CLI through a shell, which split `-ldflags "-X main.version=…"` at the
    space. It now runs programs directly.
  - **macOS smoke test in CI:** the UI loaded, but the hand edit never caused a refetch within 20 s (it does locally).
    The test used to wait a fixed 3 s for the background file watcher; the app now logs `WATCHING` when it is ready
    and `EVENT …` for every live event it forwards, and the test waits for `WATCHING`. The log of the next run will
    show whether the edit was missed by Go or by the window, if it still fails.
  - **Contract tests (all systems):** each test file built the Go server in its setup hook, which on a fresh runner
    took longer than the 10 s hook limit. The server is now built once before all files. No API behaviour failed.
- **The second CI run** confirmed the contract tests and the macOS smoke test (first screen after 2.7 s, live update
  received, 15.0 MB dmg) on GitHub's runners. Windows compiled the app but made no setup installer: `makensis` is not on
  the Windows image. The jobs now install NSIS 3.11 first.
- **The third CI run** built both Windows installers and started the app: the real UI loaded in WebView2 and fetched
  its first screen data (after 6.1 s on the CI runner). The smoke test then stopped because WebView2 also asks for
  `/favicon.ico`, which the app does not have, and the test counted that 404 as a failed request. It now counts only
  API requests.
- **The fourth CI run** passed the whole smoke test on Windows (first screen after 7.5 s on the runner, live update
  received), then failed while deleting the temporary data folder: WebView2's helper processes were still holding files.
  The test now stops the app's whole process tree on Windows and no longer fails on clean-up.
- **The first release run** (tag `v1.1.0-rc.1`) built all six Go installers, but its Windows Electron job failed in the
  Node tests, as did the Windows Node job in CI. Two tests were hit by the Node server's background "launch" backup,
  which starts 3 s after opening and on a slow runner landed mid-test (one extra backup; files still being written while
  the test folder was deleted). Re-running the job passed and published the draft. The tests now switch that backup off
  (a new `launchBackupDelayMs` option in Node, `-launch-backup=false` for the Go dev server).

## Compared with Node + Electron

macOS figures were measured on the reference machine, Linux and Windows figures come from CI runs, and Electron figures are
from phase 0 (the v1.0.3 release assets).

| Metric                               | Node + Electron                          | Go + Wails                                              | Result                                                                                                                 |
| ------------------------------------ | ---------------------------------------- | ------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------- |
| macOS installer (universal dmg)      | 231.4 MB                                 | **15.1 MB**                                             | **About 15x smaller.**                                                                                                 |
| macOS app on disk (universal)        | not measured                             | 32 MB                                                   |                                                                                                                        |
| Windows installer (setup / portable) | 112.9 MB / 112.6 MB                      | **9.1 MB / 18.1 MB**                                    | **About 12x and 6x smaller.** From the third CI run. The setup is compressed; the portable `.exe` is the bare program. |
| Linux AppImage / deb / rpm           | 127.3 / 101.1 / 90.3 MB                  | no AppImage / **7.5 / 7.7 MB** (and a 6.8 MB `.tar.gz`) | **deb about 13x and rpm about 12x smaller.** No AppImage (see above). From the first CI run.                           |
| CI time                              | Node `verify` job on Windows: 1 min 47 s | Go `go` job on Windows (vet and race tests): 1 min 28 s | About the same; one run each. Building and smoke-testing the app adds a job of about 3 min on Linux.                   |
| Go modules a developer downloads     | none                                     | 14 modules, 72 MB unpacked, plus the Wails CLI (32 MB)  | Only what the build compiles on the three systems; the whole module graph, test-only modules included, is 235 MB.      |
| `node_modules`                       | 646 MB today                             | the same, for now                                       | Still needed to build the UI. Electron alone is 312 MB of it and goes away at the cut-over (phase 7).                  |

The phase 0 page recorded `node_modules` as 339 MB. It is 646 MB today with the same lock file plus the test tools added
since; measured now, not estimated.

## Done when

- [x] Installers build for all three systems from a tag. **The `v1.1.0-rc.1` tag built every Electron and Go installer
      and made a draft pre-release with all of them. Its Go sizes match the CI figures above (dmg 15.1 MB, setup
      9.1 MB, portable 18.1 MB, deb 7.5 MB, rpm 7.8 MB, tar.gz 6.8 MB), and its Electron sizes match phase 0 to the
      byte.**
- [x] Sizes are recorded here next to the Node + Electron ones. **Done for all three.**
- [x] Unsigned-build notes are updated (`docs/PACKAGING.md`).

## Risks

- The Windows and Linux builds and the hidden-window smoke test have never run. WebView2 and WebKitGTK may behave
  differently with a hidden window; if the smoke test fails there, the first thing to try is a visible window under a
  virtual display.
