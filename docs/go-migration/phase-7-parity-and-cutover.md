# Phase 7: parity tests and cut-over

**Status:** done. Every feature maps to a test that passes against the Go app, libraries written by the Node app open in
the Go app with identical results, and the Electron app and the Node server are removed. Cipherkeep is a Go + Wails app
from version 1.1. The user tested the `v1.1.0-rc.1` pre-release before the cut-over.

## Goal

Prove the Go app does everything the Node app did, then make it the default and retire the Electron code.

## What was done

- **The last check against the Node app.** While the Node server still existed, it wrote two complete libraries through
  its own API (`fixtures/compat/node-app`): the starter pack, a Notion import, hand-made items of every kind, a note in a
  nested folder, a custom template, renamed tags, changed settings, study history and an attachment. One was then
  encrypted. Node's answers to 23 reads after reopening each library were recorded. The Go app opens both (unlocking the
  encrypted one, refusing a wrong passphrase first) and gives the same answer to every read, returns the attachment byte
  for byte, shows the same activity log, and keeps writing to them. Changing one character in the recorded answers makes
  the test fail.
- **End-to-end tests for the Go app.** The Electron tests drove the Electron window, which cannot be done with the system
  web views Wails uses. They now drive the exact UI bundle the app embeds, served with the Go API, in Playwright's
  **WebKit** (the engine of the macOS and Linux web views) and **Chromium** (close to Windows' WebView2): both on macOS
  and Linux in CI, Chromium on Windows, where no app runs on WebKit. Every step was kept: the no-network and token checks, a Notion import through the UI, search, filling
  a placeholder and copying, the command palette, five study cards from the keyboard, a lab, the dashboard, a restart,
  an encrypted library locking and unlocking, and the axe accessibility audit of all 13 pages in both themes. A new test
  checks that a hand edit on disk reaches the open page. The desktop shell itself is covered by the smoke test of the
  built app (phase 6).
- **The API contract tests stay.** The TypeScript API, vault and UI-serving tests (`tests/contract`) now run against the
  Go server only, as part of `npm test`.
- **One gap closed.** The Notion API import routes (off by default, token checks, the job and its preview, only
  `api.notion.com` contacted, the token written nowhere) were tested only by a Node test that replaced Node internals. A
  Go test now covers them.
- **The cut-over.** Removed: `apps/desktop` (Electron), `packages/server` (the Fastify API), `packages/jsonstore` (the
  TypeScript storage engine), and the scripts that only drove them (the Node benchmarks and fixture generators). CI and
  the release build only the Go app; the release names its installers `Cipherkeep-<version>-…`. The README,
  architecture, packaging, security and performance pages describe the Go app. `packages/shared` stays: the UI uses it.
  All of the removed code is in the git history.

## Parity checklist

Every feature of the app and where it is proved. "Go" is `go test ./...`, "contract" is `tests/contract`, "e2e" is the
Playwright suite, "UI" is the Vitest UI tests, "smoke" is the built-app smoke test on all three systems.

| Feature                                                              | Proved by                                                                                       |
| -------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------- |
| Command search, including dash flags and operators (`cmd:`, `tag:`)  | contract (search), Go (search, parity with TypeScript scores), e2e (search a command)           |
| Copy with one key; command palette                                   | e2e (copy syntax, palette Enter), UI (Palette)                                                  |
| Fill `<placeholders>` once; sensitive values never stored            | e2e, UI (CommandDetail, placeholder store)                                                      |
| Every collection: create, read, list, patch, delete, bulk            | contract (CRUD for all seven), Go (repositories)                                                |
| Notes as `.md` with frontmatter; folder traversal refused            | contract (storage layout), Go (notes)                                                           |
| Relations, `[[wiki links]]`, backlinks, graph                        | Go (links, parity), Node-app libraries (graph), UI (wiki links)                                 |
| Hand edits reload live                                               | Go (repositories, background watching), e2e (hand edit reaches the page), smoke                 |
| Spaced repetition, cards, typed answers                              | Go (study, parity of schedules and queues), e2e (five cards), UI (StudyPage)                    |
| Skill map, threat level, streak, recommendations, dashboard          | Go (study, overview parity in two time zones), e2e (dashboard), UI (Dashboard)                  |
| Open questions                                                       | Go (parity), Node-app libraries, UI (QuestionsPage)                                             |
| Import: Notion export, CSV, Markdown folder, JSON backup; undo       | Go (importer, parity with TypeScript plans), e2e (Notion zip through the UI)                    |
| Notion API import: off by default, token never stored                | Go (job runner with a fake Notion, and the routes)                                              |
| Export: Markdown vault, CSV, JSON; private writeups left out         | Go (exporter, parity, round trips)                                                              |
| Starter pack                                                         | Go (starter, equal to `starter-pack/`), e2e (first run), contract (vault test loads it)         |
| Templates, categories, skills, tags rename and merge                 | contract                                                                                        |
| Settings validated strictly                                          | contract                                                                                        |
| Backups and restore                                                  | contract, Go (jsonstore)                                                                        |
| Attachments                                                          | contract (vault test), Node-app libraries (byte for byte)                                       |
| Sanitized writeup export                                             | Go (sanitize, parity)                                                                           |
| Encryption at rest, recovery key, lock now, attempt limiting         | contract (vault), Go (vault, api), e2e (locked start, wrong passphrase, unlock), UI (VaultGate) |
| A library from the Node app opens, plain and encrypted               | Go (Node-app libraries), Go (jsonstore and vault compatibility fixtures)                        |
| Markdown sanitised; remote content not loaded                        | UI (MarkdownView)                                                                               |
| No network: no port, page cannot reach the internet, safe links only | e2e (remote fetch blocked), Go (desktop link rules), smoke (no port, phase 4 measurement)       |
| Accessibility: every page in both themes                             | e2e (axe, WebKit and Chromium)                                                                  |
| Data folder: OS default, override, move                              | Go (desktop: folder rules on all three systems, the move pointer, copying)                      |
| Installers for macOS, Windows and Linux                              | CI and the release (build and smoke-test on all three)                                          |

### Not covered by an automated test

- **Blur mode** (Ctrl/Cmd+Shift+B) is styling in the UI, unchanged by the migration, and has no test in either
  version.
- **Window position, menus and single instance** are Wails features configured in `go/app`. The placement rules have a
  unit test, but the native menu and the second-instance handoff were checked only by using the app.
- **Moving the data folder** from Settings: the rules and the copy are tested, the restart after it is not.

## Compared with Node + Electron

The final comparison of every phase is on the [overview page](README.md#final-comparison). Code size and dependencies,
counted just before the Node code was removed (lines that are not blank, tests excluded):

| Metric                         | Node + Electron                                                                                   | Go + Wails                                                                                                  | Result                                                                                                                                                                                                                                               |
| ------------------------------ | ------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Runtime code                   | 6,941 lines (server 5,654, storage engine 762, Electron shell 525), plus 1,702 shared with the UI | 16,130 lines (backend and shell)                                                                            | **Go is about twice as long.** It does by hand what Node took from libraries: the search engine (MiniSearch), CSV parsing (PapaParse), validation (Zod), the HTTP framework (Fastify), and the JavaScript string and sort rules the files depend on. |
| Runtime dependencies           | 64 npm packages, plus Electron (its own Chromium and Node)                                        | 24 Go modules, 6 used directly (the rest come with Wails); no runtime of its own beyond the system web view | Fewer, and nothing large: the app is one 32 MB program instead of a bundled browser.                                                                                                                                                                 |
| `node_modules` for development | 646 MB                                                                                            | 229 MB                                                                                                      | **About 65% smaller.** Node is still needed to build the UI.                                                                                                                                                                                         |

## Done when

- [x] Every item on the parity checklist passes (the three listed above are checked by hand, as in the Node version).
- [x] The final comparison table in this folder is complete, with real numbers and an honest note on every row where
      Go is not better.
- [x] The Electron app and Node server are removed, and the README describes the Go app.

## Risks that remain

- The end-to-end tests run in Playwright's WebKit and Chromium, not in the exact web views of each system; the
  built-app smoke test covers those, with fewer steps.
- **Keyboard: links and Tab.** In WebKit, as in Safari, Tab skips links unless full keyboard navigation is turned on;
  Option+Tab reaches them. The macOS app uses WebKit, so the "Skip to main content" link is reached with Option+Tab
  there, where the Electron app (Chromium) reached it with Tab. The accessibility test checks the link is the first
  keyboard stop with either key.
- The fixtures recorded from the Node version are now fixed. Behaviour that changes on purpose from here on updates the
  Go tests directly.
