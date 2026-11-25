// API benchmark (Node side): the requests of `npm run bench`, over loopback HTTP, on a copy of an existing library.
// Pairs with go/cmd/bench-api so both versions are measured on the same data:
//   npx tsx scripts/bench-api.ts <library-dir> [rounds]
// The library is the one scripts/bench-lib.ts generates (10,000 notes, 5,000 commands, ...). It prints JSON.
import { execFileSync } from 'node:child_process';
import { cpSync, mkdtempSync, rmSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { startServer } from '../packages/server/src/index.ts';

const src = path.resolve(process.argv[2] ?? 'bench-lib-data');
const ROUNDS = Number(process.argv[3] ?? 3);

export const REQUESTS: [label: string, n: number, path: (i: number) => string][] = [
  ['GET /notes?limit=50', 10, () => '/notes?limit=50'],
  ['GET /notes?limit=500 (full bodies)', 5, () => '/notes?limit=500'],
  ['GET /notes?limit=500&omit=markdown', 5, () => '/notes?limit=500&omit=markdown'],
  ['GET /commands?limit=500', 5, () => '/commands?limit=500'],
  ['search "nmap" (all types)', 20, () => '/search?q=nmap'],
  ['search "-sV" flag', 20, () => '/search?q=' + encodeURIComponent('-sV')],
  [
    'search cmd:cmd42 (5k commands)',
    20,
    (i) => `/search?q=${encodeURIComponent('cmd:cmd' + (40 + i))}`,
  ],
  ['search prefix "kern" type=notes', 20, () => '/search?q=kern&type=notes&limit=50'],
  ['GET /graph', 3, () => '/graph?includeOrphans=true'],
  ['GET /stats/overview', 3, () => '/stats/overview'],
  ['GET /notes/:id', 20, () => '/notes/{first}'],
];

const median = (xs: number[]) => [...xs].sort((a, b) => a - b)[Math.floor(xs.length / 2)]!;

function listeningSockets(): number {
  try {
    const out = execFileSync(
      'lsof',
      ['-a', '-p', String(process.pid), '-iTCP', '-sTCP:LISTEN', '-Fn'],
      {
        encoding: 'utf8',
      },
    );
    return out.split('\n').filter((l) => l.startsWith('n')).length;
  } catch {
    return 0; // lsof exits 1 when nothing matches
  }
}

const dir = mkdtempSync(path.join(os.tmpdir(), 'ck-bench-api-'));
cpSync(src, dir, { recursive: true });
const t0 = performance.now();
const s = await startServer({ dataDir: dir, watch: false });
const startMs = performance.now() - t0;
const headers = { authorization: `Bearer ${s.token}` };
const call = async (p: string) => {
  const t = performance.now();
  const r = await fetch(`${s.url}/api/v1${p}`, { headers });
  await r.arrayBuffer();
  if (r.status !== 200) throw new Error(`${p}: ${r.status}`);
  return performance.now() - t;
};
const first = (
  (await (await fetch(`${s.url}/api/v1/notes?limit=1`, { headers })).json()) as {
    items: { id: string }[];
  }
).items[0]!.id;

const rows: Record<string, number[]> = {};
for (let round = 0; round < ROUNDS; round++) {
  for (const [label, n, p] of REQUESTS) {
    const ms: number[] = [];
    for (let i = 0; i < n; i++) ms.push(await call(p(i).replace('{first}', first)));
    (rows[label] ??= []).push(median(ms));
  }
  const t = performance.now();
  const r = await fetch(`${s.url}/api/v1/notes/${first}`, {
    method: 'PATCH',
    headers: { ...headers, 'content-type': 'application/json' },
    body: JSON.stringify({ markdown: `edited ${round}` }),
  });
  await r.arrayBuffer();
  (rows['PATCH one note'] ??= []).push(performance.now() - t);
}
const sockets = listeningSockets();
const peakRss = process.resourceUsage().maxRSS * 1024; // maxRSS is in KiB
await s.close();
rmSync(dir, { recursive: true, force: true });

// Unlock: a small encrypted library at the real scrypt cost (2^15), so the time is the key derivation plus opening.
const vdir = mkdtempSync(path.join(os.tmpdir(), 'ck-bench-vault-'));
const PASS = 'benchmark passphrase';
let v = await startServer({ dataDir: vdir, watch: false });
const vcall = (method: string, p: string, body?: unknown) =>
  fetch(`${v.url}/api/v1${p}`, {
    method,
    headers: { authorization: `Bearer ${v.token}`, 'content-type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
await vcall('POST', '/commands', { name: 'ls', category: 'File System' });
await vcall('POST', '/vault/enable', { passphrase: PASS });
await v.close();
const unlock: number[] = [];
for (let round = 0; round < ROUNDS; round++) {
  v = await startServer({ dataDir: vdir, watch: false });
  const t = performance.now();
  const r = await vcall('POST', '/vault/unlock', { passphrase: PASS });
  await r.arrayBuffer();
  if (r.status !== 200) throw new Error(`unlock: ${r.status}`);
  unlock.push(performance.now() - t);
  await v.close();
}
rmSync(vdir, { recursive: true, force: true });

console.log(
  JSON.stringify(
    {
      version: 'node',
      transport: 'loopback',
      startMs,
      peakRssMB: peakRss / 1048576,
      listeningSockets: sockets,
      rows: Object.fromEntries(
        Object.entries(rows).map(([k, v]) => [k, { median: median(v), all: v }]),
      ),
      unlockMs: { median: median(unlock), all: unlock },
    },
    null,
    2,
  ),
);
