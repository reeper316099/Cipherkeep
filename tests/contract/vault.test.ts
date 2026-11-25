import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import AdmZip from 'adm-zip';
import { mkdtemp, readFile, readdir, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {
  isEncrypted,
  isEncryptedLine,
  startTestServer,
  type Json,
  type TestServer,
} from './helpers.ts';

const SENTINEL = 'ZEBRA-SENTINEL-4471';
const PASS = 'correct horse battery';
const KDF = { algo: 'scrypt', N: 1024, r: 8, p: 1 } as const; // cheap for tests; real vaults use 2^15
const RECOVERY = /^([0-9A-Z]{4}-){7}[0-9A-Z]{4}$/;
// A real 1x1 PNG so the attachment magic check passes.
const PNG = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==',
  'base64',
);

let dir: string;
let s: TestServer | undefined;
beforeEach(async () => {
  dir = await mkdtemp(path.join(os.tmpdir(), 'ck-vault-'));
});
afterEach(async () => {
  await s?.close().catch(() => undefined);
  s = undefined;
  await rm(dir, { recursive: true, force: true });
});

async function start(): Promise<TestServer> {
  s = await startTestServer({ dataDir: dir, watch: false, kdf: KDF });
  return s;
}
async function stop() {
  await s?.close();
  s = undefined;
}
async function call(method: string, p: string, body?: unknown, token?: string) {
  const res = await fetch(`${s!.url}/api/v1${p}`, {
    method,
    headers: {
      authorization: `Bearer ${token ?? s!.token}`,
      ...(body !== undefined ? { 'content-type': 'application/json' } : {}),
    },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  const text = await res.text();
  return { status: res.status, json: (text ? JSON.parse(text) : null) as Json };
}

/** Whether the library is locked, asked the way the UI asks. */
async function locked(): Promise<boolean> {
  return (await call('GET', '/vault')).json.locked as boolean;
}

async function files(root: string, skipTop: string[] = []): Promise<string[]> {
  const out: string[] = [];
  const walk = async (d: string, top: boolean) => {
    for (const e of await readdir(d, { withFileTypes: true })) {
      if (e.name.startsWith('.') || (top && skipTop.includes(e.name))) continue;
      const abs = path.join(d, e.name);
      if (e.isDirectory()) await walk(abs, false);
      else out.push(abs);
    }
  };
  await walk(root, true);
  return out;
}

/** Everything on disk that could reveal content: returns the files that are readable without a key. */
async function plaintextOffenders(): Promise<string[]> {
  const bad: string[] = [];
  const check = (name: string, raw: Buffer) => {
    if (raw.includes(Buffer.from(SENTINEL))) bad.push(`${name} (sentinel)`);
    else if (name.endsWith('.jsonl')) {
      const lines = raw.toString('utf8').split('\n').filter(Boolean);
      if (lines.some((l) => !isEncryptedLine(l))) bad.push(`${name} (plain log line)`);
    } else if (!isEncrypted(raw)) bad.push(`${name} (not encrypted)`);
  };
  for (const f of await files(dir, ['vault.json', 'meta.json', 'backups']))
    check(path.relative(dir, f), await readFile(f));
  for (const b of await files(path.join(dir, 'backups'))) {
    for (const e of new AdmZip(b).getEntries())
      if (!e.isDirectory && e.entryName !== 'meta.json' && e.entryName !== 'vault.json')
        check(`${path.basename(b)}:${e.entryName}`, e.getData());
  }
  return bad;
}

async function seed() {
  const cmd = await call('POST', '/commands', {
    name: 'zebra-cmd',
    category: 'File System',
    syntax: 'zebra-cmd [x]',
    notes: `private ${SENTINEL}`,
  });
  const note = await call('POST', '/notes', {
    title: `Secret ${SENTINEL}`,
    folder: 'Secret Folder',
    markdown: `# body\n\n${SENTINEL}\n`,
  });
  await call('POST', '/concepts', { title: 'Zebra concept', category: 'Linux' });
  await call('POST', '/study/answer', { id: cmd.json.id, rating: 'good', cardType: 'name' });
  await call('PUT', '/templates/commands', {
    name: SENTINEL,
    sections: [{ key: 'a', title: 'A' }],
  });
  const form = new FormData();
  form.append('file', new Blob([new Uint8Array(PNG)], { type: 'image/png' }), 'dot.png');
  const att = await fetch(`${s!.url}/api/v1/attachments`, {
    method: 'POST',
    headers: { authorization: `Bearer ${s!.token}` },
    body: form,
  });
  const attUrl = ((await att.json()) as { url: string }).url.replace('ck://', '/api/v1/');
  await call('POST', '/starter-pack/load', { strategy: 'skip' }); // writes imports/<id>/manifest + report
  await call('POST', '/backups', { reason: 'plain-before' });
  return { cmd: cmd.json, note: note.json, attUrl };
}

describe('encryption at rest', () => {
  it('encrypts everything on disk, survives restarts, and decrypts back to the original layout', async () => {
    await start();
    const { cmd, note, attUrl } = await seed();
    expect(await readFile(path.join(dir, 'data/commands', 'file-system.json'), 'utf8')).toContain(
      SENTINEL,
    );

    // --- enabling ---
    expect((await call('POST', '/vault/enable', { passphrase: 'short' })).json.error.code).toBe(
      'WEAK_PASSPHRASE',
    );
    const en = await call('POST', '/vault/enable', { passphrase: PASS });
    expect(en.status).toBe(200);
    expect(en.json.recoveryKey).toMatch(RECOVERY);
    expect(await plaintextOffenders()).toEqual([]);
    expect(await readdir(path.join(dir, 'data/notes'))).toEqual(['_enc']);
    expect((await readdir(path.join(dir, 'backups'))).length).toBe(1); // old plaintext backups are gone

    // The running app is unaffected: reads, writes, search, attachments.
    expect((await call('GET', `/notes/${note.id}`)).json.markdown).toContain(SENTINEL);
    expect((await call('GET', `/notes/${note.id}`)).json.folder).toBe('secret-folder');
    expect((await call('GET', '/search?q=ZEBRA')).json.hits.length).toBeGreaterThan(0);
    const att = await fetch(`${s!.url}${attUrl}`, {
      headers: { authorization: `Bearer ${s!.token}` },
    });
    expect(Buffer.from(await att.arrayBuffer()).equals(PNG)).toBe(true);
    const created = await call('POST', '/notes', { title: `New ${SENTINEL}`, markdown: SENTINEL });
    await call('PATCH', `/notes/${created.json.id}`, { title: 'Renamed', folder: 'Other' });
    await call('POST', '/study/answer', { id: cmd.id, rating: 'easy', cardType: 'name' });
    await call('POST', '/backups', { reason: 'after' });
    expect(await plaintextOffenders()).toEqual([]);

    // --- restart: locked ---
    await stop();
    await start();
    expect(await locked()).toBe(true);
    expect((await call('GET', '/vault')).json).toMatchObject({ enabled: true, locked: true });
    const denied = await call('GET', '/commands');
    expect(denied).toMatchObject({ status: 423, json: { error: { code: 'LOCKED' } } });
    expect((await call('GET', '/health')).json).toMatchObject({ locked: true });
    expect((await call('GET', '/commands', undefined, 'wrong-token')).status).toBe(401);
    const wrong = await call('POST', '/vault/unlock', { passphrase: 'not the passphrase' });
    expect(wrong).toMatchObject({ status: 401, json: { error: { code: 'BAD_CREDENTIALS' } } });
    expect((await call('GET', '/commands')).status).toBe(423);
    expect(await plaintextOffenders()).toEqual([]);

    expect((await call('POST', '/vault/unlock', { passphrase: PASS })).status).toBe(200);
    expect(await locked()).toBe(false);
    expect((await call('GET', `/notes/${note.id}`)).json.markdown).toContain(SENTINEL);
    expect((await call('GET', `/commands/${cmd.id}`)).json).toMatchObject({ practiceCount: 2 });
    expect((await call('GET', `/notes/${created.json.id}`)).json).toMatchObject({
      title: 'Renamed',
      folder: 'other',
    });
    expect((await call('GET', '/study/stats')).status).toBe(200);

    // --- lock now ---
    expect((await call('POST', '/vault/lock')).status).toBe(200);
    expect((await call('GET', '/commands')).status).toBe(423);
    expect((await call('POST', '/vault/unlock', { passphrase: PASS })).status).toBe(200);

    // --- disabling ---
    const bad = await call('POST', '/vault/disable', { passphrase: 'nope nope nope' });
    expect(bad.status).toBe(401);
    expect((await call('POST', '/vault/disable', { passphrase: PASS })).status).toBe(200);
    await expect(readFile(path.join(dir, 'vault.json'))).rejects.toThrow();
    const folder = await readdir(path.join(dir, 'data/notes/secret-folder'));
    expect(folder).toHaveLength(1);
    expect(
      await readFile(path.join(dir, 'data/notes/secret-folder', folder[0]!), 'utf8'),
    ).toContain(SENTINEL);
    expect(await readdir(path.join(dir, 'data/notes'))).not.toContain('_enc');
    for (const f of await files(dir, ['backups']))
      expect(isEncrypted(await readFile(f)), path.relative(dir, f)).toBe(false);

    await stop();
    await start();
    expect(await locked()).toBe(false);
    expect((await call('GET', `/notes/${note.id}`)).json).toMatchObject({
      folder: 'secret-folder',
    });
  }, 60_000);

  it('changes the passphrase without rewriting data, and recovers with the recovery key', async () => {
    await start();
    const { note } = await seed();
    const en = await call('POST', '/vault/enable', { passphrase: PASS });
    const before = await readFile(path.join(dir, 'data/notes/_enc', `${note.id}.md`));

    expect(
      (
        await call('POST', '/vault/change-passphrase', {
          current: 'wrong wrong wrong',
          next: 'x'.repeat(12),
        })
      ).status,
    ).toBe(401);
    expect(
      (await call('POST', '/vault/change-passphrase', { current: PASS, next: 'short' })).json.error
        .code,
    ).toBe('WEAK_PASSPHRASE');
    const NEW = 'a brand new passphrase';
    expect(
      (await call('POST', '/vault/change-passphrase', { current: PASS, next: NEW })).status,
    ).toBe(200);
    expect(
      (await readFile(path.join(dir, 'data/notes/_enc', `${note.id}.md`))).equals(before),
    ).toBe(true);

    await stop();
    await start();
    expect((await call('POST', '/vault/unlock', { passphrase: PASS })).status).toBe(401);
    expect((await call('POST', '/vault/unlock', { passphrase: NEW })).status).toBe(200);
    await stop();
    await start();
    // Recovery key: needs a new passphrase to finish.
    const noNew = await call('POST', '/vault/unlock', {
      recoveryKey: en.json.recoveryKey.toLowerCase().replaceAll('-', ' '),
    });
    expect(noNew).toMatchObject({
      status: 400,
      json: { error: { code: 'NEW_PASSPHRASE_REQUIRED' } },
    });
    const NEWER = 'recovered passphrase';
    expect(
      (
        await call('POST', '/vault/unlock', {
          recoveryKey: en.json.recoveryKey,
          newPassphrase: NEWER,
        })
      ).status,
    ).toBe(200);
    expect((await call('GET', `/notes/${note.id}`)).json.markdown).toContain(SENTINEL);

    // A new recovery key replaces the old one.
    const rk = await call('POST', '/vault/recovery-key', { passphrase: NEWER });
    expect(rk.json.recoveryKey).toMatch(RECOVERY);
    expect(rk.json.recoveryKey).not.toBe(en.json.recoveryKey);
    await stop();
    await start();
    expect(
      (
        await call('POST', '/vault/unlock', {
          recoveryKey: en.json.recoveryKey,
          newPassphrase: NEWER,
        })
      ).status,
    ).toBe(401);
    expect(
      (
        await call('POST', '/vault/unlock', {
          recoveryKey: rk.json.recoveryKey,
          newPassphrase: NEWER,
        })
      ).status,
    ).toBe(200);
  }, 60_000);

  it('restoring a plaintext backup into an encrypted library leaves nothing readable', async () => {
    await start();
    const { note } = await seed();
    const olderBackups = (await call('GET', '/backups')).json.map((b: Json) => b.name as string);
    await call('POST', '/vault/enable', { passphrase: PASS, deleteOldBackups: false });
    const plain = (await call('GET', '/backups')).json.find(
      (b: Json) => b.reason === 'plain-before',
    );
    expect(plain).toBeTruthy();
    expect((await call('POST', '/backup/restore', { name: plain.name })).status).toBe(200);
    expect((await call('GET', `/notes/${note.id}`)).json.markdown).toContain(SENTINEL);
    // Backups made before encryption stay as they were (the user chose to keep them); nothing else may be readable.
    const offenders = (await plaintextOffenders()).filter(
      (o) => !olderBackups.some((n: string) => o.startsWith(`${n}:`)),
    );
    expect(offenders).toEqual([]);
  }, 60_000);

  it('slows down repeated wrong guesses', async () => {
    await start();
    await call('POST', '/vault/enable', { passphrase: PASS });
    await stop();
    await start();
    for (let i = 0; i < 6; i++)
      expect((await call('POST', '/vault/unlock', { passphrase: `wrong guess ${i}` })).status).toBe(
        401,
      );
    expect((await call('POST', '/vault/unlock', { passphrase: PASS })).status).toBe(429);
  }, 60_000);

  it('a library that was never encrypted is unaffected', async () => {
    await start();
    expect((await call('GET', '/vault')).json).toMatchObject({ enabled: false, locked: false });
    expect((await call('POST', '/vault/unlock', { passphrase: PASS })).status).toBe(404);
  });
});
