// Storage-engine benchmark (Node side). Pairs with go/cmd/bench-store so the two engines are measured on the same
// on-disk data:   npx tsx scripts/bench-store.ts <dataset-dir>        (the dataset is generated if missing)
import { execFileSync } from 'node:child_process';
import {
  cpSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readdirSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { promises as fs } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { AesGcmCodec, BackupManager, JsonStore } from '../packages/jsonstore/src/index.ts';

const KEY = Buffer.from(Array.from({ length: 32 }, (_, i) => i));
const SHARDS = 100;
const PER_SHARD = 50;
const NOTES = 10_000;
const dataset = path.resolve(process.argv[2] ?? 'bench-data');

interface Cmd {
  id: string;
  name: string;
  category: string;
  syntax: string;
  explanation: string;
  examples: { command: string; description: string }[];
  tags: string[];
  mastery: number;
}
interface Shard {
  items: Cmd[];
}
const opts = { version: 1, defaults: (): Shard => ({ items: [] }) };

const cmd = (i: number): Cmd => ({
  id: `cmd${String(i).padStart(5, '0')}`,
  name: `tool${i}`,
  category: `cat-${i % SHARDS}`,
  syntax: `tool${i} [-a] [-b <value>] <target>`,
  explanation:
    `Explains what tool${i} does in a sentence or two, with enough text to be realistic. `
      .repeat(3)
      .trim(),
  examples: [
    { command: `tool${i} -a 10.0.0.${i % 255}`, description: 'scan the host' },
    { command: `tool${i} -b value target`, description: 'use a value' },
  ],
  tags: ['bench', `group${i % 20}`],
  mastery: i % 6,
});
const note = (i: number) =>
  `---\nid: n${i}\ntitle: Note ${i}\ntags: [bench, t${i % 30}]\ncreatedAt: 2026-01-01T00:00:00.000Z\nupdatedAt: 2026-01-01T00:00:00.000Z\n---\n# Note ${i}\n\n` +
  `Paragraph about topic ${i % 97} with a few words to make the file about a kilobyte long. `.repeat(
    12,
  ) +
  '\n';

async function generate() {
  for (const kind of ['plain', 'encrypted'] as const) {
    const dir = path.join(dataset, kind);
    const store = new JsonStore(dir);
    if (kind === 'encrypted') store.codec.set(new AesGcmCodec(KEY));
    for (let s = 0; s < SHARDS; s++) {
      const f = await store.open(`data/commands/cat-${s}.json`, opts);
      await f.update(
        (d) =>
          void d.items.push(...Array.from({ length: PER_SHARD }, (_, k) => cmd(s * PER_SHARD + k))),
      );
    }
    const enc = new AesGcmCodec(KEY);
    for (let i = 0; i < NOTES; i++) {
      const p = path.join(dir, 'data/notes', `area-${i % 20}`, `topic-${i % 10}`, `note-${i}.md`);
      mkdirSync(path.dirname(p), { recursive: true });
      writeFileSync(p, kind === 'encrypted' ? enc.encodeText(note(i)) : note(i));
    }
  }
}

const rssMb = () =>
  Number(execFileSync('ps', ['-o', 'rss=', '-p', String(process.pid)], { encoding: 'utf8' })) /
  1024;
const median = (xs: number[]) => [...xs].sort((a, b) => a - b)[Math.floor(xs.length / 2)]!;

async function walk(dir: string, out: string[] = []): Promise<string[]> {
  for (const e of await fs.readdir(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) await walk(p, out);
    else out.push(p);
  }
  return out;
}

async function run(kind: 'plain' | 'encrypted') {
  const tmp = mkdtempSync(path.join(os.tmpdir(), 'ck-bs-'));
  cpSync(path.join(dataset, kind), tmp, { recursive: true });
  const store = new JsonStore(
    tmp,
    new BackupManager({ dataDir: tmp, backupDir: path.join(tmp, 'backups') }),
  );
  if (kind === 'encrypted') store.codec.set(new AesGcmCodec(KEY));
  const res: Record<string, number> = {};

  let t = performance.now();
  const files = [];
  for (let s = 0; s < SHARDS; s++)
    files.push(await store.open<Shard>(`data/commands/cat-${s}.json`, opts));
  res['open 100 shards (5,000 commands) ms'] = performance.now() - t;
  if (files.reduce((n, f) => n + f.data.items.length, 0) !== SHARDS * PER_SHARD)
    throw new Error('bad load');

  // Read the dataset in place (no per-run copy of 10,000 fresh files, which the OS indexes and scans unevenly):
  // one warm-up pass, then five timed passes; the median and the best are reported.
  const notes = await walk(path.join(dataset, kind, 'data/notes'));
  const readAll = async () => {
    let bytes = 0;
    let next = 0;
    await Promise.all(
      Array.from({ length: 48 }, async () => {
        while (next < notes.length) {
          const f = notes[next++]!;
          const plain = store.codec.decode(await fs.readFile(f));
          bytes += plain.length; // after the await: `bytes += await …` would use a stale value and lose updates
        }
      }),
    );
    return bytes;
  };
  await readAll();
  const passes: number[] = [];
  let bytes = 0;
  for (let i = 0; i < 5; i++) {
    t = performance.now();
    bytes = await readAll();
    passes.push(performance.now() - t);
  }
  if (notes.length !== NOTES) throw new Error('bad scan');
  res['read + decode 10,000 note files, median of 5 warm passes ms'] = median(passes);
  res['read + decode 10,000 note files, best of 5 ms'] = Math.min(...passes);
  res['(bytes decoded)'] = bytes;

  const times: number[] = [];
  for (let i = 0; i < 40; i++) {
    t = performance.now();
    await files[i % SHARDS]!.update((d) => void (d.items[0]!.mastery = i));
    times.push(performance.now() - t);
  }
  res['update one 50-command shard, median ms'] = median(times);

  t = performance.now();
  await store.backups!.create('bench');
  res['zip backup of the whole library ms'] = performance.now() - t;
  res['memory after load, RSS MB'] = rssMb();
  rmSync(tmp, { recursive: true, force: true });
  return res;
}

if (
  !existsSync(path.join(dataset, 'plain')) ||
  readdirSync(path.join(dataset, 'plain')).length === 0
)
  await generate();
for (const kind of ['plain', 'encrypted'] as const) {
  const runs = [];
  for (let i = 0; i < 3; i++) runs.push(await run(kind));
  const keys = Object.keys(runs[0]!);
  console.log(
    JSON.stringify({
      engine: 'node',
      kind,
      results: Object.fromEntries(keys.map((k) => [k, +median(runs.map((r) => r[k]!)).toFixed(1)])),
    }),
  );
}
