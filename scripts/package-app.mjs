// Builds the Go + Wails desktop app and its installers for the current system into ./release-go:
//   macOS    Cipherkeep-Go-<v>-macos-universal.dmg (arm64 + x86_64)
//   Windows  Cipherkeep-Go-<v>-windows-amd64-setup.exe (NSIS) and -portable.exe (the bare program)
//   Linux    Cipherkeep-Go-<v>-linux-amd64.deb / .rpm (nfpm) and .tar.gz
// Needs Go, Node (for the UI build) and the Wails CLI; NSIS on Windows; nfpm and the WebKitGTK 4.1 dev packages on
// Linux. Builds are unsigned (see docs/PACKAGING.md).
//   node scripts/package-app.mjs [version]        (default: the version in apps/desktop/package.json)
import { execFileSync } from 'node:child_process';
import {
  copyFileSync,
  existsSync,
  mkdirSync,
  readFileSync,
  rmSync,
  statSync,
  symlinkSync,
  writeFileSync,
} from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const APP = path.join(ROOT, 'go', 'app');
const BIN = path.join(APP, 'build', 'bin');
const OUT = path.join(ROOT, 'release-go');
const version =
  process.argv[2]?.replace(/^v/, '') ??
  JSON.parse(readFileSync(path.join(ROOT, 'apps/desktop/package.json'), 'utf8')).version;
const name = `Cipherkeep-Go-${version}`;
const run = (cmd, args, cwd = APP, env = {}) =>
  execFileSync(cmd, args, {
    cwd,
    stdio: 'inherit',
    env: { ...process.env, ...env },
    // No shell, so arguments with spaces (-ldflags "-X main.version=…") reach the program whole on Windows too.
  });
const ldflags = `-X main.version=${version}`;

// wails.json carries the version shown in Info.plist and the Windows file properties.
const wailsJson = path.join(APP, 'wails.json');
const original = readFileSync(wailsJson, 'utf8');
const cfg = JSON.parse(original);
cfg.info.productVersion = version.replace(/-.*$/, '');
writeFileSync(wailsJson, JSON.stringify(cfg, null, 2) + '\n');

rmSync(OUT, { recursive: true, force: true });
mkdirSync(OUT, { recursive: true });
rmSync(BIN, { recursive: true, force: true });
const made = [];
try {
  if (process.platform === 'darwin') {
    const appDir = path.join(BIN, 'Cipherkeep.app');
    try {
      run('wails', ['build', '-platform', 'darwin/universal', '-ldflags', ldflags]);
    } catch (e) {
      // The Wails CLI sometimes fails its own ad-hoc signing step ("replacing existing signature") after the app is
      // fully built. Sign it here instead; a broken build still fails the verification below.
      if (!existsSync(path.join(appDir, 'Contents/MacOS/Cipherkeep'))) throw e;
      run('codesign', ['--force', '--deep', '--sign', '-', appDir], ROOT);
    }
    run('codesign', ['--verify', '--deep', appDir], ROOT);
    const stage = path.join(OUT, 'dmg');
    mkdirSync(stage);
    run('cp', ['-R', path.join(BIN, 'Cipherkeep.app'), stage], ROOT);
    symlinkSync('/Applications', path.join(stage, 'Applications'));
    const dmg = path.join(OUT, `${name}-macos-universal.dmg`);
    run(
      'hdiutil',
      ['create', '-volname', 'Cipherkeep', '-srcfolder', stage, '-ov', '-format', 'UDZO', dmg],
      ROOT,
    );
    rmSync(stage, { recursive: true, force: true });
    made.push(dmg);
  } else if (process.platform === 'win32') {
    run('wails', ['build', '-platform', 'windows/amd64', '-nsis', '-ldflags', ldflags]);
    const setup = path.join(OUT, `${name}-windows-amd64-setup.exe`);
    const portable = path.join(OUT, `${name}-windows-amd64-portable.exe`);
    copyFileSync(path.join(BIN, 'Cipherkeep-amd64-installer.exe'), setup);
    copyFileSync(path.join(BIN, 'Cipherkeep.exe'), portable);
    made.push(setup, portable);
  } else {
    run('wails', ['build', '-platform', 'linux/amd64', '-tags', 'webkit2_41', '-ldflags', ldflags]);
    for (const fmt of ['deb', 'rpm']) {
      const target = path.join(OUT, `${name}-linux-amd64.${fmt}`);
      run(
        'nfpm',
        [
          'package',
          '--config',
          path.join(APP, 'build/linux/nfpm.yaml'),
          '--packager',
          fmt,
          '--target',
          target,
        ],
        APP,
        { VERSION: version.replace(/-/g, '~') },
      );
      made.push(target);
    }
    const tgz = path.join(OUT, `${name}-linux-amd64.tar.gz`);
    run('tar', ['-czf', tgz, '-C', BIN, 'Cipherkeep'], ROOT);
    made.push(tgz);
  }
} finally {
  writeFileSync(wailsJson, original);
}
for (const f of made)
  console.log(`${path.basename(f).padEnd(52)} ${(statSync(f).size / 1e6).toFixed(1)} MB`);
