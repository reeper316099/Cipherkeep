// Measures both desktop apps the same way (macOS): launch to the moment the UI has its dashboard data
// (GET /api/v1/stats/overview answered), then memory after 10 s idle, summed over every process the app uses.
// It opens real windows, so run it only when nobody is using the machine:
//   npm run desktop:build                                   # the Electron app (unpackaged, as in phase 0)
//   (cd go/app && wails build)                              # the Wails app
//   node scripts/measure-desktop.mjs [10k-library-dir] [rounds]
// The library dir is the one scripts/bench-lib.ts generates; without it only the empty library is measured.
import { _electron as electron } from '@playwright/test';
import { execFileSync, spawn } from 'node:child_process';
import { cpSync, existsSync, mkdtempSync, readFileSync, rmSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const WAILS = path.join(ROOT, 'go/app/build/bin/Cipherkeep.app/Contents/MacOS/Cipherkeep');
const LIB = process.argv[2] ? path.resolve(process.argv[2]) : undefined;
const ROUNDS = Number(process.argv[3] ?? 3);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function processes() {
  return execFileSync('ps', ['-axo', 'pid=,ppid=,rss=,comm='], { encoding: 'utf8' })
    .trim()
    .split('\n')
    .map((l) => {
      const m = /^\s*(\d+)\s+(\d+)\s+(\d+)\s+(.*)$/.exec(l);
      return { pid: +m[1], ppid: +m[2], rss: +m[3], comm: m[4] };
    });
}
/** The app's own process tree, plus WebKit helpers that appeared since `before` (they are launchd's children). */
function memory(rootPid, before) {
  const rows = processes();
  const pids = new Set([rootPid]);
  for (let grew = true; grew;) {
    grew = false;
    for (const r of rows)
      if (pids.has(r.ppid) && !pids.has(r.pid)) {
        pids.add(r.pid);
        grew = true;
      }
  }
  for (const r of rows)
    if (/com\.apple\.WebKit\./.test(r.comm) && !before.has(r.pid)) pids.add(r.pid);
  const mine = rows.filter((r) => pids.has(r.pid));
  return { mb: Math.round(mine.reduce((s, r) => s + r.rss, 0) / 1024), procs: mine.length };
}
const webkitNow = () =>
  new Set(
    processes()
      .filter((r) => /com\.apple\.WebKit\./.test(r.comm))
      .map((r) => r.pid),
  );

function dataDir(withLib) {
  const d = mkdtempSync(path.join(os.tmpdir(), 'ck-measure-'));
  if (withLib) cpSync(LIB, d, { recursive: true });
  return d;
}

async function wails(withLib) {
  const dir = dataDir(withLib);
  const log = path.join(dir, '..', path.basename(dir) + '.log');
  const before = webkitNow();
  const t0 = performance.now();
  const p = spawn(WAILS, [], {
    env: { ...process.env, CIPHERKEEP_DATA_DIR: dir, CIPHERKEEP_API_LOG: log },
    stdio: 'ignore',
  });
  let ms;
  for (let i = 0; i < 3000 && ms === undefined; i++) {
    if (existsSync(log) && readFileSync(log, 'utf8').includes('/api/v1/stats/overview'))
      ms = performance.now() - t0;
    else await sleep(10);
  }
  await sleep(10_000);
  const mem = memory(p.pid, before);
  const exited = new Promise((r) => p.once('exit', r));
  p.kill('SIGTERM');
  await exited;
  rmSync(dir, { recursive: true, force: true });
  rmSync(log, { force: true });
  return { ms: Math.round(ms), ...mem };
}

async function electronApp(withLib) {
  const dir = dataDir(withLib);
  const before = webkitNow(); // Electron starts no WebKit helpers; this keeps other apps' ones out of its total
  const t0 = performance.now();
  const app = await electron.launch({
    args: [path.join(ROOT, 'apps/desktop')],
    env: { ...process.env, CIPHERKEEP_DATA_DIR: dir },
  });
  const page = await app.firstWindow();
  await page.waitForResponse((r) => r.url().includes('/api/v1/stats/overview'), {
    timeout: 60_000,
  });
  const ms = performance.now() - t0;
  await sleep(10_000);
  const mem = memory(app.process().pid, before);
  await app.close();
  rmSync(dir, { recursive: true, force: true, maxRetries: 10, retryDelay: 200 });
  return { ms: Math.round(ms), ...mem };
}

const out = {};
for (const lib of LIB ? [false, true] : [false]) {
  for (const [name, fn] of [
    ['electron', electronApp],
    ['wails', wails],
  ]) {
    const key = `${name}-${lib ? '10k' : 'empty'}`;
    out[key] = [];
    for (let i = 0; i < ROUNDS; i++) out[key].push(await fn(lib));
  }
}
console.log(JSON.stringify(out, null, 2));
