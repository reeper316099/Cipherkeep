import { execFileSync } from 'node:child_process';
import { rmSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

/**
 * Builds what the end-to-end tests run once: the UI exactly as the desktop app embeds it (`--mode wails`, into
 * go/app/frontend/dist) and the Go API server (go/cmd/ck-server), which serves that UI and the API on a loopback port.
 */
export default function globalSetup(): () => void {
  execFileSync('npm', ['run', 'build:wails', '--workspace', '@cipherkeep/web'], {
    cwd: ROOT,
    stdio: 'inherit',
    shell: process.platform === 'win32', // npm is a .cmd script on Windows
  });
  const bin = path.join(
    os.tmpdir(),
    `ck-e2e-server-${process.pid}${process.platform === 'win32' ? '.exe' : ''}`,
  );
  execFileSync('go', ['build', '-o', bin, './cmd/ck-server'], {
    cwd: path.join(ROOT, 'go'),
    stdio: 'inherit',
  });
  process.env.CK_E2E_SERVER = bin;
  process.env.CK_E2E_UI = path.join(ROOT, 'go/app/frontend/dist');
  return () => rmSync(bin, { force: true });
}
