# Phase 5: Wails shell and UI

**Status:** done on macOS. The Go + Wails app runs the real UI against the in-process API, and start time and memory are
measured for both versions. Windows and Linux compile and vet, but the app has not been run on them yet; that is part of
phase 6 (packaging and CI on each system).

## Goal

Wrap the Go backend and the existing React UI in a Wails window.

## What was built

| Path                               | What it does                                                                                                                                     |
| ---------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------ |
| `go/app`                           | The Wails app: window, menu, single instance, saved window size, and the functions the UI may call. `wails.json` builds the UI from `apps/web`   |
| `go/internal/desktop`              | The platform-independent shell logic, ported from `apps/desktop/src`: where the data lives, which links may leave the app, the data-folder rules |
| `apps/web/src/lib/wails-bridge.ts` | Builds the `window.cipherkeep` object the UI already uses from the Wails bindings, so the UI code is unchanged apart from a few lines            |
| `scripts/measure-desktop.mjs`      | Measures both apps the same way (below)                                                                                                          |

Build with `npm run app:build` (the Wails CLI), or `npm run app:dev` for hot reload.

## Design notes

- **No port, no token.** The UI is served by the Wails asset server, and every request it does not find in the bundle
  (everything under `/api/v1`) goes to the Go API handler inside the process. `lsof` shows no listening socket.
- **The UI barely changed.** `window.cipherkeep` used to come from the Electron preload. In the Wails app a small bridge
  builds the same object from the Go bindings (open link, show in folder, pick a folder, move the data folder, data
  folder path, menu actions). The only other UI changes: no `Authorization` header when there is no token, and live
  updates come from a Wails event instead of the event stream.
- **Live updates use Wails events.** The shell subscribes to the library's events and forwards them to the window.
  Streaming responses through the asset server work on macOS, but I could not confirm them on the other web views, so
  the app does not depend on them. The SSE route still exists for the loopback server.
- **File watching starts after the library is shown.** On macOS the watcher needs one handle per watched file, and
  setting it up for 10,000 notes took several seconds. The desktop app now opens the library, shows it, and starts
  watching in the background; a hand edit made in those first seconds is not picked up. Tests and `ck-server` keep the
  old order.
- **Network policy.** Electron could cancel every request that left the app. Wails has no such hook, so the rule is
  enforced by the page's Content-Security-Policy, which in the Wails build allows connections only to the app's own
  origin (`connect-src 'self'`; the Electron build still allows its loopback port). External links are never followed
  inside the window: clicks on web and mail links, and `window.open`, go to the system browser through Go, which accepts
  only `http`, `https` and `mailto` links without credentials.
- **Same data folder rules.** The default folder, the `CIPHERKEEP_DATA_DIR` override and the "move data folder" pointer
  follow the Electron app's rules, so both apps open the same library. The shell's own files live in `.desktop-session`,
  which backups leave out (in both versions).

### What is different from the Electron app

- **macOS menu:** Wails v2 cannot add items to the standard application menu, so **Settings…** (⌘,) moved to the File
  menu. The View menu has no zoom or full-screen items (Wails v2 has no roles for them).
- **Restoring the window position:** Wails reports screen sizes but not their positions, so a saved position is checked
  against screen sizes only. On a multi-monitor setup the window may open on the main screen instead.
- **Web view:** the system web view (WebKit on macOS) instead of Chromium. The UI was checked on macOS only.

## How it was tested

- **Go tests** for the shell logic: data-folder resolution on all three systems, the move pointer, safe links, the
  data-folder rules, copying a library without its lock, window placement; and for background watching (a hand edit is
  still picked up, and closing while the watch starts is safe).
- **UI test** for the bridge: Wails origins are recognised, only foreign web and mail links leave the window, the bridge
  maps every function and event, and it does nothing in Electron or the dev browser.
- **A smoke test of the built app with a hidden window** (`CIPHERKEEP_START_HIDDEN=1`, `CIPHERKEEP_API_LOG`, a temporary
  data folder): the real UI loaded, made its first API calls in process with no token, a note written to disk by hand
  reached the window as an event and the UI refetched, no port was open, and quitting released the library lock. It was
  repeated after the network policy was tightened.
- Everything from the earlier phases still passes: all Go tests with the race detector, the Node and UI tests, and the
  TypeScript API tests run against the Go server.

## Compared with Node + Electron

Measured with `scripts/measure-desktop.mjs` on the reference machine, both apps visible, one after the other, three
launches each. The clock runs from starting the app to the UI receiving its dashboard data (`GET /api/v1/stats/overview`
answered: the moment the first screen can draw), the same end point for both. Memory is summed over every process the app
uses after 10 s idle: Electron's process tree, and for Wails the app plus the WebKit helper processes it started (macOS
runs those outside the app's own tree). Electron is the unpackaged build, as in phase 0; Wails is the built `.app`. The
10k library is the one from phases 2 and 4 (10,000 notes, 5,000 commands, 200 concepts, 100 labs, 150 log entries).

| Metric                                | Node + Electron           | Go + Wails                | Result                                                          |
| ------------------------------------- | ------------------------- | ------------------------- | --------------------------------------------------------------- |
| Launch to first screen, empty library | 593 ms (583 to 1,709)     | 513 ms (472 to 1,285)     | Go about 14% faster. Both first launches were cold and slow.    |
| Idle memory, empty library            | 510 MB (485 to 512)       | 257 MB (254 to 259)       | **Go uses about half.**                                         |
| Launch to first screen, 10k library   | 4,384 ms (4,217 to 4,559) | 1,482 ms (1,221 to 2,267) | **Go about 3x faster.**                                         |
| Idle memory, 10k library              | 896 MB (891 to 902)       | 481 MB (470 to 518)       | **Go uses about half.**                                         |
| Processes                             | 4                         | 4                         | The same count: Wails' web view runs in three WebKit helpers.   |
| Listening network ports               | 1 (plus a token)          | 0                         | No port at all.                                                 |
| App size (macOS, universal, unpacked) | not measured here         | 32 MB                     | The installer comparison (Electron's dmg is 231 MB) is phase 6. |

Figures are medians of three launches with the range in brackets.

### About these numbers

- **The empty-library launch is close.** Most of half a second goes to starting the web view and loading the UI bundle,
  which neither backend changes much. The first launch of each app was much slower (cold caches), so the medians use
  the two warm launches and the cold one together.
- **The 10k launch is where the backend shows.** Electron spends about four seconds loading and indexing before the
  dashboard can answer; Go does the same work in about a second. The first Wails measurement, before file watching was
  moved to the background, took 3.2 to 4.7 s: no better than Electron. It is reported here because it is why the
  watcher change was made.
- **An error in the first run of the script.** It counted every WebKit process on the machine towards Electron (10
  processes instead of 4). That was fixed and the whole measurement run again; only the second run is in the table.
- **Not measured:** Windows and Linux (phase 6), and memory after long use.

### What this phase shows

The Wails app uses about half the memory of the Electron app, opens a large library about three times faster, and listens
on no port. With an empty library the start is about the same, a little quicker. The cost is a few small menu
differences and a web view that differs per system, which is what phase 6 has to check on Windows and Linux.

## Done when

- [ ] The app runs on macOS, Windows and Linux with the real UI. **macOS only so far;** Windows and Linux in phase 6.
- [x] Start time and idle memory are recorded for both versions.
- [x] The web view cannot reach the network (connections limited to its own origin by the page's policy; external links
      go to the system browser).

## Risks that remain

- Windows (WebView2) and Linux (WebKitGTK) have not been run. CSS and web view differences may show up there.
- The network policy relies on the page's Content-Security-Policy alone; Electron also had a request filter in the
  main process.
