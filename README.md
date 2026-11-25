# Cipherkeep

An offline, local-first desktop app for cybersecurity learning notes: a command reference you can copy
from, concepts, a lab/CTF log, projects, a learning log, cheat sheets and snippets. It also helps you
remember what you learn, with spaced-repetition review, a skill map and a dashboard.

**No accounts, no telemetry, no AI.** Your data is plain JSON and Markdown files in a folder on your
computer. The only optional thing that uses the internet is the Notion API import, and it is off by
default.

## What it does

- **Command library**: instant search (dash flags like `-la` and `--color=auto` work), copy with one key,
  fill in `<placeholders>` once and every example updates. Values you mark sensitive are never stored.
- **Concepts, labs, projects, learning log, notes, snippets**: editors with relations, `[[wiki links]]`,
  backlinks and a graph. Notes are real `.md` files with frontmatter.
- **Study**: spaced-repetition review, a skill map computed from your practice, recommendations that say
  why, a streak and heatmap, and a threat-level banner on the dashboard.
- **Import and export**: import a Notion export (zip), CSV, a Markdown folder or a JSON backup, with a
  preview first, merge strategies and one-click undo. Export an Obsidian-compatible Markdown vault, CSV or
  JSON.
- **Starter pack**: original, MIT-licensed content (commands, concepts, guides, practice labs) you can load
  on first run. It never overwrites what you wrote and can be undone.
- **Privacy tools**: blur mode for screen sharing (Ctrl/Cmd+Shift+B), lab flags not stored by default, and
  optional encryption at rest.
- **Optional encryption at rest**: AES-256 with a passphrase and a recovery key. If you lose both, the data
  cannot be recovered. See [docs/SECURITY.md](docs/SECURITY.md).
- **Optional Notion API import**: read a Notion workspace directly with an integration token (used for the
  import only, never saved).
- **Accessible**: keyboard-friendly, a skip link, dark and light themes, and an automated axe audit of every
  page.
- **Fast and small**: tested with 10,000 notes and 5,000 commands; about half the memory of the old Electron version.
  See [docs/PERFORMANCE.md](docs/PERFORMANCE.md).

The app never runs commands or scripts you store.

## Install

Download the installer for your system from the [releases page](https://github.com/reeper316099/Cipherkeep/releases):
a universal `.dmg` for macOS (Apple silicon and Intel), a setup or portable `.exe` for Windows, and a `.deb`, `.rpm` or
`.tar.gz` for Linux. They are 7 to 18 MB. Builds are **unsigned**, so macOS and Windows warn about an unidentified
developer on first launch. See [docs/PACKAGING.md](docs/PACKAGING.md) for how to open them and how to add signing.

The app uses the web view your system already has: WebKit on macOS, WebView2 on Windows (built into Windows 11 and
current Windows 10) and WebKitGTK 4.1 on Linux (the `.deb` and `.rpm` pull it in).

Your data lives in the OS app-data folder (change it in Settings, or set `CIPHERKEEP_DATA_DIR`). Back it up
by copying the folder. See [docs/DATA_FORMAT.md](docs/DATA_FORMAT.md). Libraries made by the earlier Electron version
open unchanged, encrypted ones included.

## Develop

You need Go (the version in `go/go.mod`), Node 20 or newer (to build the UI), and the
[Wails](https://wails.io) v2 CLI (`go install github.com/wailsapp/wails/v2/cmd/wails@v2.17.1`). On Linux, also
`libgtk-3-dev` and `libwebkit2gtk-4.1-dev`.

```bash
npm install
npm run app:dev              # the desktop app with hot reload
npm run app:build            # build it (go/app/build/bin)
npm run app:package          # installers for this system (./release)
npm run dev                  # Go API on :5174 (data in .dev-data/) + the UI on http://localhost:5173
npm test                     # UI, shared and API contract tests (Vitest; the contract tests run the Go server)
(cd go && go test -race ./...)   # the Go tests
npm run e2e                  # the UI and the Go API in WebKit and Chromium (Playwright)
npm run lint && npm run typecheck
npm run fixtures:generate    # rebuild the synthetic Notion export
```

In the desktop app the UI's `/api/v1` calls are answered by the Go API inside the process: there is no network port.
In dev the Vite server proxies `/api` to the Go dev server (`go/cmd/ck-server`) and adds its token.

## Layout

| Path                 | Purpose                                                                               |
| -------------------- | ------------------------------------------------------------------------------------- |
| `go/app`             | The desktop app: Wails window, menu, and the bridge the UI uses for desktop actions   |
| `go/internal`        | The backend: storage engine, repositories, search, study, import/export, API, vault   |
| `go/cmd/ck-server`   | The API on a loopback port, for development and tests                                 |
| `apps/web`           | React frontend                                                                        |
| `packages/shared`    | Constants, schemas and logic the UI uses (study engine, wiki links, Notion helpers)   |
| `starter-pack/`      | Original MIT starter content (embedded in the app)                                    |
| `fixtures/synthetic` | Generated fake Notion-style export used by tests                                      |
| `fixtures/compat`    | Libraries and expected results from the Node version, which the Go version must match |
| `tests/contract`     | The API contract tests, run against the Go server                                     |
| `e2e/`               | Playwright tests of the UI and the Go API                                             |
| `private-fixtures/`  | Your real export. **Gitignored; never commit.**                                       |

Cipherkeep was a Node + Electron app until version 1.1; [docs/go-migration](docs/go-migration/README.md) describes the
move to Go + Wails and measures it against the old version.

## Docs

- [Architecture](docs/ARCHITECTURE.md)
- [Data format](docs/DATA_FORMAT.md)
- [Security and privacy](docs/SECURITY.md)
- [Notion import](docs/NOTION_IMPORT.md)
- [Study engine](docs/STUDY_ENGINE.md)
- [Performance](docs/PERFORMANCE.md)
- [Packaging](docs/PACKAGING.md)
- [Moving to Go + Wails](docs/go-migration/README.md)

## Keeping the public repo clean

A pre-commit hook and CI run `npm run guard`. It fails if anything resembling a personal Notion export is
staged outside `fixtures/synthetic/` or `starter-pack/`, or if a file looks like it contains an API key or
private key. Never put real notes, lab details or tokens in code, tests, docs or commit messages.

## License

MIT
