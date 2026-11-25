// Smoke test of the built Go + Wails app, with its window hidden, so it can run in CI or in the background:
//   1. start it on an empty, temporary data folder;
//   2. wait for the real UI to load and ask the in-process API for its first screen (GET /api/v1/stats/overview);
//   3. once the app logs WATCHING, write a note to disk by hand and wait for the UI to refetch (the live-event path:
//      the app logs EVENT when Go sees the change, then the UI's refetch shows up as requests);
//   4. quit it and check it released the library lock.
// It reads the app's request log (CIPHERKEEP_API_LOG: method, path and status, plus WATCHING and EVENT <kind> lines;
// never content).
//   node scripts/smoke-app.mjs [path-to-binary]      (default: this system's build in go/app/build/bin)
import { spawn } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const BIN = path.join(ROOT, 'go/app/build/bin');
const binary =
  process.argv[2] ??
  {
    darwin: path.join(BIN, 'Cipherkeep.app/Contents/MacOS/Cipherkeep'),
    win32: path.join(BIN, 'Cipherkeep.exe'),
  }[process.platform] ??
  path.join(BIN, 'Cipherkeep');

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const dir = mkdtempSync(path.join(os.tmpdir(), 'ck-smoke-'));
const data = path.join(dir, 'data');
const log = path.join(dir, 'api.log');
const lines = () => (existsSync(log) ? readFileSync(log, 'utf8').split('\n').filter(Boolean) : []);
async function waitFor(what, test, ms) {
  for (const end = Date.now() + ms; Date.now() < end; await sleep(50)) if (test()) return;
  throw new Error(`never happened: ${what}\n--- request log ---\n${lines().join('\n')}`);
}

const t0 = Date.now();
const app = spawn(binary, [], {
  env: {
    ...process.env,
    CIPHERKEEP_DATA_DIR: data,
    CIPHERKEEP_API_LOG: log,
    CIPHERKEEP_START_HIDDEN: '1',
  },
  stdio: ['ignore', 'inherit', 'inherit'],
});
let exitCode = null;
app.on('exit', (c) => (exitCode = c));
try {
  await waitFor(
    'the UI asked for its first screen',
    () => lines().some((l) => / GET \/api\/v1\/stats\/overview 200 /.test(l)),
    90_000,
  );
  console.log(`first screen data after ${Date.now() - t0} ms`);
  const bad = lines().filter((l) => / [45]\d\d /.test(l));
  if (bad.length) throw new Error(`failed requests:\n${bad.join('\n')}`);

  // The file watcher starts in the background after the first screen; the app logs WATCHING when it is ready.
  await waitFor(
    'the app started watching for hand edits',
    () => lines().includes('WATCHING') || lines().some((l) => / WATCHING$/.test(l)),
    60_000,
  );
  const before = lines().length;
  mkdirSync(path.join(data, 'data/notes'), { recursive: true });
  writeFileSync(
    path.join(data, 'data/notes/hand-edit.md'),
    '---\nid: 01JABCDEFGHJKMNPQRSTVWXYZ0\ntitle: Hand edit\ncreatedAt: 2026-01-01T00:00:00.000Z\nupdatedAt: 2026-01-01T00:00:00.000Z\n---\nwritten on disk\n',
  );
  await waitFor(
    'the UI refetched after a hand edit',
    () =>
      lines()
        .slice(before)
        .some((l) => /\/api\/v1\/(meta|stats\/overview) 200/.test(l)),
    20_000,
  );
  console.log('live update reached the UI');
  if (exitCode !== null) throw new Error(`the app exited early (${exitCode})`);
} finally {
  if (exitCode === null) {
    const exited = new Promise((r) => app.once('exit', r));
    app.kill(process.platform === 'win32' ? undefined : 'SIGTERM');
    await Promise.race([exited, sleep(10_000)]);
  }
}
// A forced stop on Windows cannot clean up, so the lock check is for macOS and Linux.
if (process.platform !== 'win32' && existsSync(path.join(data, '.lock')))
  throw new Error('the library lock was not released on quit');
rmSync(dir, { recursive: true, force: true, maxRetries: 10, retryDelay: 200 });
console.log('smoke test passed');
