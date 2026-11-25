# Packaging and releases

## Build locally

```bash
npm run desktop:build   # web UI + Electron main/preload (API server bundled in)
npm run desktop:start   # run it
npm run desktop:dist    # installers for the current OS into ./release
npm run e2e             # Playwright against the built app
```

`CIPHERKEEP_E2E_BIN=/path/to/Cipherkeep npx playwright test` runs the same end-to-end test against an installed or packaged build.

Targets: Windows NSIS installer + portable `.exe`; macOS universal `.dmg` (arm64 + x86_64); Linux AppImage, `.deb`, `.rpm`. Pushing a `v*` tag runs `.github/workflows/release.yml`, which builds all of them and attaches them to a **draft** release. CI (`ci.yml`) runs lint, types, unit tests and the Electron e2e on all three OSes for every push.

## The Go + Wails app (being migrated to, see docs/go-migration)

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@v2.17.1   # once
npm run app:build                                          # the app, into go/app/build/bin
node scripts/package-app.mjs [version]                     # installers for this OS into ./release-go
node scripts/smoke-app.mjs                                 # start it hidden and check the UI talks to the API
```

Targets: macOS universal `.dmg`; Windows NSIS setup and portable `.exe`; Linux `.deb`, `.rpm` and `.tar.gz` (no AppImage:
WebKitGTK cannot be bundled reliably). Linux builds need `libgtk-3-dev`, `libwebkit2gtk-4.1-dev` and `nfpm`. The Go
installers are named `Cipherkeep-Go-…` and are built by the same tag as the Electron ones until the cut-over.

The Go app has no network port at all: the UI's API calls are answered inside the process. Unsigned builds show the same
warnings as below. On Windows the app needs the WebView2 runtime (built into Windows 11 and current Windows 10); the setup
installer fetches it from Microsoft only when it is missing, and the portable `.exe` needs it already installed. Signing
later: on macOS, sign the `.app` with a Developer ID (`codesign --options runtime`) before `hdiutil` and notarise the
dmg; on Windows, sign `Cipherkeep.exe` and the setup with `signtool`.

## How the app is put together

The Electron main process starts the Fastify API on `127.0.0.1` with an ephemeral port and a random per-launch bearer token, and serves the built web UI from the same origin. The preload script (sandboxed, context-isolated) hands the page that token and a handful of validated actions (open an http(s)/mailto link, reveal a file in the file manager, change the data folder). The window cannot load anything from the network: every request to another origin is cancelled, permission prompts are denied, new windows and navigations away are blocked. See `SECURITY.md` once written, and `apps/desktop/src/security.ts` for the rules.

## Data folder

`%APPDATA%\Cipherkeep` · `~/Library/Application Support/Cipherkeep` · `$XDG_CONFIG_HOME/Cipherkeep` (or `~/.config/Cipherkeep`). Electron's own files (Chromium profile, single-instance socket, window position) live in the hidden `.electron-session` folder inside it and are excluded from backups. Override with `CIPHERKEEP_DATA_DIR` (the whole app then lives in that folder) or Settings → Data folder → Change… (optionally copies your data, then restarts).

## Unsigned builds: what users will see

- **macOS**: Gatekeeper says the app "can't be opened because Apple cannot check it". Right-click → Open once, or `xattr -dr com.apple.quarantine /Applications/Cipherkeep.app`.
- **Windows**: SmartScreen shows "Windows protected your PC". "More info" → "Run anyway".
- **Linux**: AppImage needs `chmod +x`; `.deb`/`.rpm` install normally.

There is no auto-update (no network access in v1), so a new version means installing over the old one; your data folder is separate and untouched.

## Adding signing later

1. **macOS**: an Apple Developer ID Application certificate. In `electron-builder.yml` remove `identity: null` and `hardenedRuntime: false`, set `hardenedRuntime: true`, add `notarize: true`, and provide `CSC_LINK`, `CSC_KEY_PASSWORD`, `APPLE_ID`, `APPLE_APP_SPECIFIC_PASSWORD`, `APPLE_TEAM_ID` as repository secrets passed to the "Package" step.
2. **Windows**: a code-signing certificate (EV for instant SmartScreen reputation) or Azure Trusted Signing. Set `CSC_LINK`/`CSC_KEY_PASSWORD` (or the `azureSignOptions` block).
3. **Linux**: optionally sign the repository metadata for `.deb`/`.rpm` when publishing a package repo.

Never commit certificates or passwords; keep them in GitHub secrets.
