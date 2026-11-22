import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { cpSync, mkdtempSync, readFileSync, rmSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { AesGcmCodec, BackupManager, JsonStore } from '../src/index.ts';

// fixtures/compat/go is written by the Go storage engine (UPDATE_COMPAT=1 go test, in go/). These tests prove the
// TypeScript engine reads what Go wrote; the Go tests prove the other direction.
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../../fixtures/compat');
const key = Buffer.from(readFileSync(path.join(root, 'KEY.hex'), 'utf8').trim(), 'hex');
const opts = {
  version: 1,
  defaults: () => ({ items: [] as { id: string; n: number; name: string; tags: string[] }[] }),
};

let dir: string;
beforeEach(() => void (dir = mkdtempSync(path.join(os.tmpdir(), 'ck-gocompat-'))));
afterEach(() => rmSync(dir, { recursive: true, force: true }));

const expectItems = (items: { id: string; n: number; name: string; tags: string[] }[]) => {
  expect(items.map((i) => i.id)).toEqual(['c1', 'c2', 'c3']);
  expect(items[0]!.name).toBe('ünïcode <tag> & "quotes"   end');
  expect(items[1]).toMatchObject({ n: 2.5, tags: [] });
  expect(items[2]!.name).toBe('😀 emoji and \\ backslash\nnewline');
};

describe('files written by the Go engine', () => {
  it('a plain Go shard is byte-identical to the Node one and loads', async () => {
    cpSync(path.join(root, 'go/plain'), dir, { recursive: true });
    expect(readFileSync(path.join(dir, 'data/commands/a.json'), 'utf8')).toBe(
      readFileSync(path.join(root, 'node/plain/data/commands/a.json'), 'utf8'),
    );
    const f = await new JsonStore(dir).open('data/commands/a.json', opts);
    expectItems(f.data.items);
  });

  it('an encrypted Go shard and log decrypt with the shared key', async () => {
    cpSync(path.join(root, 'go/encrypted'), dir, { recursive: true });
    const s = new JsonStore(dir);
    s.codec.set(new AesGcmCodec(key));
    expectItems((await s.open('data/commands/a.json', opts)).data.items);
    const c = new AesGcmCodec(key);
    const lines = readFileSync(path.join(dir, 'activity/2026-01.jsonl'), 'utf8').trim().split('\n');
    expect(lines.map((l) => JSON.parse(c.decodeLine(l)).n)).toEqual([1, 2, 3]);
    // Locked: refused, never quarantined.
    await expect(new JsonStore(dir).open('data/commands/a.json', opts)).rejects.toMatchObject({
      code: 'ENCRYPTED',
    });
  });

  it('a Go-made backup zip restores a corrupt file in Node', async () => {
    cpSync(path.join(root, 'go/backups'), path.join(dir, 'backups'), { recursive: true });
    const backups = new BackupManager({ dataDir: dir, backupDir: path.join(dir, 'backups') });
    expect((await backups.list())[0]?.reason).toBe('compat');
    const f = await new JsonStore(dir, backups).open('data/commands/a.json', opts); // no file on disk: defaults
    expect(f.data.items).toEqual([]);
    cpSync(path.join(root, 'go/backups'), path.join(dir, 'backups'), { recursive: true });
    const raw = await backups.readFromLatest('data/commands/a.json');
    expect(raw?.toString('utf8')).toBe(
      readFileSync(path.join(root, 'node/plain/data/commands/a.json'), 'utf8'),
    );
  });
});
