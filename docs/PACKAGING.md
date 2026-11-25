# Packaging and releases

## Build locally

You need Go, Node and the Wails v2 CLI (`go install github.com/wailsapp/wails/v2/cmd/wails@v2.17.1`). Linux also needs
`libgtk-3-dev`, `libwebkit2gtk-4.1-dev` and [nfpm](https://nfpm.goreleaser.com); Windows needs NSIS for the setup
installer.

```bash
npm run app:build                 # the app, into go/app/build/bin
npm run app:package -- [version]  # installers for this system into ./release (default version: package.json)
npm run app:smoke                 # start the built app hidden and check the UI talks to the API
```

Targets: macOS universal `.dmg` (arm64 + x86_64); Windows NSIS setup and portable `.exe`; Linux `.deb`, `.rpm` and
`.tar.gz`. There is no AppImage: WebKitGTK cannot be bundled into one reliably, so Linux builds use the system's.

Pushing a `v*` tag runs `.github/workflows/release.yml`: it builds every installer on its own system, smoke-tests the
built app, and attaches the installers to a **draft** release with a table of their sizes. A tag with a suffix
(`v1.1.0-rc.1`) is marked as a pre-release. CI (`ci.yml`) builds and smoke-tests the app on all three systems for every
push, next to the tests.

## How the app is put together

One process: the Go backend and the UI in a Wails window. The UI's `/api/v1` calls are answered by the Go API handler
inside the process, so the app opens no network port. The page reaches the Go side otherwise only through a few
bindings, each checking its input: open an `http`, `https` or `mailto` link in the browser, show a file in the file
manager (never open or run it), pick a folder, move the data folder. The page's Content-Security-Policy allows
connections only to the app's own origin, and links to other sites open in the system browser, never in the window.
See [SECURITY.md](SECURITY.md).

The app uses the system web view: WebKit on macOS, WebView2 on Windows (built into Windows 11 and current Windows 10;
if it is missing, the setup installer downloads Microsoft's runtime installer, and the portable `.exe` will not start)
and WebKitGTK 4.1 on Linux (a dependency of the `.deb` and `.rpm`).

## Data folder

`%APPDATA%\Cipherkeep` · `~/Library/Application Support/Cipherkeep` · `$XDG_CONFIG_HOME/Cipherkeep` (or
`~/.config/Cipherkeep`). The app's own files (window position, the data-folder pointer, and on Windows the web view's
profile) live in the hidden `.desktop-session` folder inside it and are left out of backups, as is `.electron-session`
from the old Electron version. Override with `CIPHERKEEP_DATA_DIR` (the whole app then lives in that folder) or
Settings → Data folder → Change… (optionally copies your data, then restarts).

## Unsigned builds: what users will see

- **macOS**: Gatekeeper says the app "can't be opened because Apple cannot check it". Right-click → Open once, or
  `xattr -dr com.apple.quarantine /Applications/Cipherkeep.app`.
- **Windows**: SmartScreen shows "Windows protected your PC". "More info" → "Run anyway".
- **Linux**: `.deb` and `.rpm` install normally; the `.tar.gz` holds the program, which needs the GTK 3 and WebKitGTK
  4.1 runtime packages.

There is no auto-update (no network access), so a new version means installing over the old one; your data folder is
separate and untouched.

## Adding signing later

1. **macOS**: an Apple Developer ID Application certificate. Sign the `.app` with
   `codesign --force --deep --options runtime --sign "Developer ID Application: …"` before `hdiutil` packs the dmg in
   `scripts/package-app.mjs`, then notarise the dmg with `xcrun notarytool submit --wait` and staple it. Provide the
   certificate and Apple credentials as repository secrets to the release job.
2. **Windows**: a code-signing certificate (EV for instant SmartScreen reputation) or Azure Trusted Signing. Sign
   `Cipherkeep.exe` before the NSIS step and the setup after it with `signtool`.
3. **Linux**: optionally sign the repository metadata for `.deb`/`.rpm` when publishing a package repo.

Never commit certificates or passwords; keep them in GitHub secrets.
