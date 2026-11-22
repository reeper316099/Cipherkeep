import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { mkdtemp, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { z } from 'zod';
import {
  BackupManager,
  JsonStore,
  acquireInstanceLock,
  type JsonFileOptions,
} from '../src/index.ts';

let dir: string;
beforeEach(async () => {
  dir = await mkdtemp(path.join(os.tmpdir(), 'jsonstore-'));
});
afterEach(async () => {
  await rm(dir, { recursive: true, force: true });
});

const schema = z.object({ items: z.array(z.object({ id: z.string(), n: z.number() })) });
type D = z.infer<typeof schema>;
const opts = (over: Partial<JsonFileOptions<D>> = {}): JsonFileOptions<D> => ({
  version: 1,
  defaults: () => ({ items: [] }),
  validator: schema,
  ...over,
});
const mk = (keep = 20) =>
  new BackupManager({ dataDir: dir, backupDir: path.join(dir, 'backups'), keep });

describe('JsonStore', () => {
  it('creates defaults, persists updates, and serialises concurrent writers', async () => {
    const store = new JsonStore(dir);
    const f = await store.open('data/a.json', opts());
    await Promise.all(
      Array.from({ length: 25 }, (_, i) =>
        f.update((d) => void d.items.push({ id: `i${i}`, n: i })),
      ),
    );
    expect(f.data.items).toHaveLength(25);
    const onDisk = JSON.parse(await readFile(path.join(dir, 'data/a.json'), 'utf8'));
    expect(onDisk.schemaVersion).toBe(1);
    expect(onDisk.data.items).toHaveLength(25);
    expect((await readdir(path.join(dir, 'data'))).filter((n) => n.endsWith('.tmp'))).toEqual([]);
  });

  it('rejects invalid updates without touching disk or memory', async () => {
    const store = new JsonStore(dir);
    const f = await store.open('a.json', opts());
    await expect(
      f.update((d) => void d.items.push({ id: 'x', n: 'bad' as never })),
    ).rejects.toThrow();
    expect(f.data.items).toEqual([]);
  });

  it('runs migrations in order and takes a pre-migration backup', async () => {
    await writeFile(
      path.join(dir, 'a.json'),
      JSON.stringify({ schemaVersion: 1, data: { list: [{ id: 'a', n: 1 }] } }),
    );
    const backups = mk();
    const store = new JsonStore(dir, backups);
    const f = await store.open('a.json', {
      ...opts(),
      version: 2,
      migrations: { 1: (d) => ({ items: (d as { list: unknown[] }).list }) },
    });
    expect(f.data.items).toEqual([{ id: 'a', n: 1 }]);
    expect(JSON.parse(await readFile(path.join(dir, 'a.json'), 'utf8')).schemaVersion).toBe(2);
    expect((await backups.list())[0]?.reason).toBe('pre-migration');
  });

  it('refuses files from a newer schema without clobbering them', async () => {
    const text = JSON.stringify({ schemaVersion: 9, data: {} });
    await writeFile(path.join(dir, 'a.json'), text);
    const store = new JsonStore(dir);
    await expect(store.open('a.json', opts())).rejects.toMatchObject({ code: 'VERSION_TOO_NEW' });
    expect(await readFile(path.join(dir, 'a.json'), 'utf8')).toBe(text);
  });

  it('quarantines a corrupt file and restores the last good backup', async () => {
    const backups = mk();
    const s1 = new JsonStore(dir, backups);
    const f1 = await s1.open('a.json', opts());
    await f1.update((d) => void d.items.push({ id: 'keep', n: 1 }));
    await backups.create('test');
    await writeFile(path.join(dir, 'a.json'), '{ "schemaVersion": 1, "data": {oops');

    const s2 = new JsonStore(dir, backups);
    const events: unknown[] = [];
    s2.on('quarantine', (e) => events.push(e));
    const f2 = await s2.open('a.json', opts());
    expect(f2.data.items).toEqual([{ id: 'keep', n: 1 }]);
    expect(events).toHaveLength(1);
    const q = await readdir(path.join(dir, '.quarantine'));
    expect(q).toHaveLength(1);
    expect(await readFile(path.join(dir, '.quarantine', q[0]!), 'utf8')).toContain('oops');
  });

  it('falls back to defaults (never throws) when corrupt and no backup exists', async () => {
    await writeFile(path.join(dir, 'a.json'), 'not json');
    const f = await new JsonStore(dir).open('a.json', opts());
    expect(f.data.items).toEqual([]);
  });

  it('reloads valid hand edits and keeps memory on invalid ones', async () => {
    const store = new JsonStore(dir);
    const f = await store.open('a.json', opts());
    await store.watch();
    const reloaded = new Promise<void>((r) => store.once('reload', () => r()));
    await writeFile(
      path.join(dir, 'a.json'),
      JSON.stringify({ schemaVersion: 1, data: { items: [{ id: 'hand', n: 5 }] } }),
    );
    await reloaded;
    expect(f.data.items[0]?.id).toBe('hand');

    const invalid = new Promise<{ line?: number; rel: string }>((r) => store.once('invalid', r));
    await writeFile(path.join(dir, 'a.json'), '{\n  "schemaVersion": 1,\n  "data": {\n  oops\n}');
    const info = await invalid;
    expect(info.rel).toBe('a.json');
    expect(f.data.items[0]?.id).toBe('hand');
    await store.close();
  });

  it('does not treat its own writes as external changes', async () => {
    const store = new JsonStore(dir);
    const f = await store.open('a.json', opts());
    await store.watch();
    let reloads = 0;
    store.on('reload', () => reloads++);
    await f.update((d) => void d.items.push({ id: 'x', n: 1 }));
    await new Promise((r) => setTimeout(r, 400));
    expect(reloads).toBe(0);
    await store.close();
  });

  it('rolls back a two-file transaction when the second write fails', async () => {
    const store = new JsonStore(dir);
    const a = await store.open('a.json', opts());
    const b = await store.open(
      'b.json',
      opts({
        validate: (d) => {
          if (d.items.some((i) => i.n === 99)) throw new Error('nope');
        },
      }),
    );
    await a.update((d) => void d.items.push({ id: 'm', n: 1 }));
    await expect(
      store.transaction([a, b], ([da, db]) => {
        da!.items = [];
        db!.items.push({ id: 'm', n: 99 });
      }),
    ).rejects.toThrow('nope');
    expect(a.data.items).toHaveLength(1);
    expect(JSON.parse(await readFile(path.join(dir, 'a.json'), 'utf8')).data.items).toHaveLength(1);
    await store.transaction([a, b], ([da, db]) => {
      db!.items.push(...da!.items);
      da!.items = [];
    });
    expect(a.data.items).toHaveLength(0);
    expect(b.data.items).toHaveLength(1);
  });

  it('blocks path traversal', async () => {
    const store = new JsonStore(dir);
    await expect(store.open('../evil.json', opts())).rejects.toMatchObject({
      code: 'PATH_TRAVERSAL',
    });
  });
});

describe('BackupManager', () => {
  it('fails cleanly, not with an uncaught exception, when a file vanishes during a backup', async () => {
    const m = mk();
    await writeFile(path.join(dir, 'a.json'), '{}');
    const big = path.join(dir, 'gone.json');
    await writeFile(big, '{}');
    const orig = m['collect' as keyof BackupManager] as unknown as (
      ...a: unknown[]
    ) => Promise<unknown>;
    // Make the file list include a path that no longer exists, as if it was deleted after listing.
    (m as unknown as { collect: (...a: unknown[]) => Promise<unknown> }).collect = async (...a) => [
      ...((await orig.apply(m, a)) as { abs: string; entry: string }[]),
      { abs: path.join(dir, 'missing.json'), entry: 'missing.json' },
    ];
    await expect(m.create('race')).rejects.toThrow(/ENOENT/);
    expect(await readdir(path.join(dir, 'backups')).catch(() => [])).not.toContain('race');
  });

  it('keeps only the newest N and skips fresh launch backups', async () => {
    const b = mk(3);
    await writeFile(path.join(dir, 'x.json'), '1');
    for (let i = 0; i < 5; i++) await b.create(`r${i}`);
    expect(await b.list()).toHaveLength(3);
    expect(await b.createIfStale(6)).toBeNull();
  });
});

describe('instance lock', () => {
  it('rejects a second holder and takes over a stale lock', async () => {
    const lock = await acquireInstanceLock(dir);
    await expect(acquireInstanceLock(dir)).rejects.toMatchObject({ code: 'LOCKED' });
    await lock.release();
    await writeFile(path.join(dir, '.lock'), JSON.stringify({ pid: 2 ** 22 + 12345 }));
    const again = await acquireInstanceLock(dir);
    await again.release();
  });
});

describe('watching a folder with unwatchable files', () => {
  it.skipIf(process.platform === 'win32')(
    'starts even when the data folder contains a unix socket (Electron’s SingletonSocket)',
    async () => {
      const net = await import('node:net');
      const sock = path.join(dir, 'SingletonSocket');
      const srv = net.createServer().listen(sock);
      await new Promise((r) => srv.once('listening', r));
      try {
        const store = new JsonStore(dir);
        const f = await store.open('a.json', opts());
        const t0 = Date.now();
        await store.watch(); // must resolve promptly instead of hanging or throwing
        expect(Date.now() - t0).toBeLessThan(4000);
        await f.update((d) => void d.items.push({ id: 'x', n: 1 }));
        await store.close();
      } finally {
        srv.close();
      }
    },
  );
});
