// Writes the fixtures the Go storage engine is tested against (fixtures/compat/node/…) using the TypeScript
// jsonstore, with a fixed test key. Re-run only when the on-disk format changes:  npx tsx scripts/gen-compat-fixtures.ts
// The key is public test data; it protects nothing.
import { cpSync, mkdirSync, readdirSync, renameSync, rmSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { AesGcmCodec, BackupManager, JsonStore } from '../packages/jsonstore/src/index.ts';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', 'fixtures', 'compat');
const out = path.join(root, 'node');
const KEY = Buffer.from(Array.from({ length: 32 }, (_, i) => i));

export const DATA = {
  items: [
    { id: 'c1', n: 1, name: 'ünïcode <tag> & "quotes"   end', tags: ['a', 'b'] },
    { id: 'c2', n: 2.5, name: 'second', tags: [] as string[] },
    { id: 'c3', n: 3, name: '😀 emoji and \\ backslash\nnewline', tags: ['z'] },
  ],
};
const opts = { version: 1, defaults: () => ({ items: [] as typeof DATA.items }) };

rmSync(out, { recursive: true, force: true });
mkdirSync(out, { recursive: true });
writeFileSync(path.join(root, 'KEY.hex'), KEY.toString('hex') + '\n');

for (const kind of ['plain', 'encrypted'] as const) {
  const dir = path.join(out, kind);
  mkdirSync(dir, { recursive: true });
  const store = new JsonStore(dir);
  if (kind === 'encrypted') store.codec.set(new AesGcmCodec(KEY));
  const f = await store.open('data/commands/a.json', opts);
  await f.update((d) => void d.items.push(...DATA.items));
  if (kind === 'encrypted') {
    const c = new AesGcmCodec(KEY);
    mkdirSync(path.join(dir, 'activity'), { recursive: true });
    const lines = [1, 2, 3].map((i) =>
      c.encodeLine(JSON.stringify({ ts: `2026-01-0${i}T00:00:00.000Z`, type: 'create', n: i })),
    );
    writeFileSync(path.join(dir, 'activity', '2026-01.jsonl'), lines.join('\n') + '\n');
  }
}

// A Node-made backup of the plain library, with a fixed name.
const tmp = path.join(out, '_tmp');
cpSync(path.join(out, 'plain'), tmp, { recursive: true });
const backups = new BackupManager({ dataDir: tmp, backupDir: path.join(out, 'backups') });
const b = await backups.create('compat');
renameSync(b.file, path.join(out, 'backups', '20260101T000000-compat.zip'));
rmSync(tmp, { recursive: true, force: true });
console.log(readdirSync(out), 'written to', out);
