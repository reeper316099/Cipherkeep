import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import http from 'node:http';
import { readFile, readdir } from 'node:fs/promises';
import path from 'node:path';
import { COLLECTIONS } from '@cipherkeep/shared';
import { harness, SAMPLES, type Harness } from './helpers.ts';

let h: Harness;
beforeAll(async () => {
  h = await harness();
});
afterAll(() => h.stop());

describe('auth', () => {
  it('rejects missing and wrong tokens on every collection route', async () => {
    for (const [method, p] of [
      ['GET', '/health'],
      ['GET', '/meta'],
      ['GET', '/search?q=x'],
      ['GET', '/settings'],
      ...COLLECTIONS.flatMap((c) => [
        ['GET', `/${c}`],
        ['POST', `/${c}`],
        ['GET', `/${c}/x`],
        ['DELETE', `/${c}/x`],
      ]),
    ] as [string, string][]) {
      expect((await h.req(method, p, method === 'POST' ? {} : undefined, null)).status).toBe(401);
      expect((await h.req(method, p, method === 'POST' ? {} : undefined, 'wrong')).status).toBe(
        401,
      );
    }
  });

  it('rejects non-loopback Host headers', async () => {
    const status = await new Promise<number>((resolve, reject) => {
      const url = new URL(h.s.url);
      const r = http.request(
        {
          host: url.hostname,
          port: url.port,
          path: '/api/v1/health',
          headers: { host: 'evil.example', authorization: `Bearer ${h.s.token}` },
        },
        (res) => {
          res.resume();
          resolve(res.statusCode ?? 0);
        },
      );
      r.on('error', reject);
      r.end();
    });
    expect(status).toBe(403);
  });

  it('serves health with a request id', async () => {
    const r = await h.req('GET', '/health');
    expect(r.status).toBe(200);
    expect(r.headers.get('x-request-id')).toBeTruthy();
  });
});

describe('errors', () => {
  it('uses one error shape with request id', async () => {
    const r = await h.req('POST', '/commands', { category: 'x' });
    expect(r.status).toBe(400);
    expect(r.json.error).toMatchObject({
      code: 'VALIDATION',
      requestId: r.headers.get('x-request-id'),
    });
    const nf = await h.req('GET', '/commands/nope');
    expect(nf.status).toBe(404);
    expect(nf.json.error.code).toBe('NOT_FOUND');
    expect((await h.req('GET', '/nope')).status).toBe(404);
  });
});

describe.each(COLLECTIONS)('CRUD: %s', (name) => {
  it('creates, reads, lists, patches, deletes', async () => {
    const created = await h.req('POST', `/${name}`, SAMPLES[name]);
    expect(created.status).toBe(201);
    const id = created.json.id as string;
    expect(id).toHaveLength(26);
    expect(created.json.slug).toBeTruthy();
    expect((await h.req('GET', `/${name}/${id}`)).json.id).toBe(id);
    const list = await h.req('GET', `/${name}`);
    expect(list.json.items.some((e: { id: string }) => e.id === id)).toBe(true);
    const patched = await h.req('PATCH', `/${name}/${id}`, { favorite: true, tags: ['t1'] });
    expect(patched.json).toMatchObject({ favorite: true, tags: ['t1'], id });
    expect(Date.parse(patched.json.updatedAt)).toBeGreaterThanOrEqual(
      Date.parse(created.json.updatedAt),
    );
    expect(
      (await h.req('GET', `/${name}?favorite=true`)).json.items.map((e: { id: string }) => e.id),
    ).toContain(id);
    expect((await h.req('DELETE', `/${name}/${id}`)).status).toBe(204);
    expect((await h.req('GET', `/${name}/${id}`)).status).toBe(404);
  });

  it('supports bulk patch and delete (with a pre-delete backup)', async () => {
    const ids: string[] = [];
    for (let i = 0; i < 3; i++)
      ids.push(
        (
          await h.req('POST', `/${name}`, {
            ...SAMPLES[name],
            title: `bulk ${i}`,
            name: `bulk${i}`,
          })
        ).json.id,
      );
    expect(
      (await h.req('POST', `/${name}/bulk`, { op: 'patch', ids, patch: { archived: true } })).json
        .affected,
    ).toBe(3);
    expect((await h.req('GET', `/${name}/${ids[0]}`)).json.archived).toBe(true);
    expect((await h.req('POST', `/${name}/bulk`, { op: 'delete', ids })).json.affected).toBe(3);
    expect(
      (await h.req('GET', '/backups')).json.some(
        (b: { reason: string }) => b.reason === 'pre-bulk-delete',
      ),
    ).toBe(true);
    expect((await h.req('POST', `/${name}/bulk`, { op: 'delete', ids: ['missing'] })).status).toBe(
      404,
    );
  });
});

describe('storage layout', () => {
  it('moves a command between category shard files', async () => {
    const c = (await h.req('POST', '/commands', { name: 'mover', category: 'File System' })).json;
    const dir = path.join(h.dir, 'data/commands');
    expect(await readdir(dir)).toContain('file-system.json');
    await h.req('PATCH', `/commands/${c.id}`, { category: 'Networking' });
    const net = JSON.parse(await readFile(path.join(dir, 'networking.json'), 'utf8'));
    expect(net.schemaVersion).toBe(1);
    expect(net.data.items.map((e: { id: string }) => e.id)).toContain(c.id);
    const fs = JSON.parse(await readFile(path.join(dir, 'file-system.json'), 'utf8'));
    expect(fs.data.items.map((e: { id: string }) => e.id)).not.toContain(c.id);
  });

  it('writes notes as .md with frontmatter and rejects traversal', async () => {
    const n = (
      await h.req('POST', '/notes', { title: 'Local DNS', folder: 'Guides/Net', markdown: 'hello' })
    ).json;
    const text = await readFile(path.join(h.dir, 'data/notes/guides/net/local-dns.md'), 'utf8');
    expect(text).toMatch(/^---\n/);
    expect(text).toContain(`id: ${n.id}`);
    expect(text.endsWith('hello')).toBe(true);
    const bad = await h.req('POST', '/notes', { title: 'x', folder: '../../etc' });
    expect(bad.status).toBe(400);
    const dup = (await h.req('POST', '/notes', { title: 'Local DNS', folder: 'Guides/Net' })).json;
    expect(dup.slug).toBe('local-dns-2');
  });

  it('records activity without content', async () => {
    const month = new Date().toISOString().slice(0, 7);
    const ev = (await h.req('GET', `/activity/${month}`)).json as { type: string; id: string }[];
    expect(ev.length).toBeGreaterThan(0);
    expect(JSON.stringify(ev)).not.toContain('echo hi');
  });
});

describe('search', () => {
  beforeAll(async () => {
    await h.req('POST', '/commands', {
      name: 'lsdemo',
      category: 'File System',
      tags: ['linux'],
      whatItDoes: 'lists directory contents',
      options: [
        { flag: '-la', meaning: 'long format including hidden' },
        { flag: '--color=auto', meaning: 'colorize' },
      ],
      examples: [{ command: 'ls -la ~', description: 'long list of home' }],
    });
    await h.req('POST', '/notes', {
      title: 'Cheat',
      markdown: 'mentions lsdemo only in the body',
      tags: ['linux'],
    });
  });

  it('finds dash flags and ranks name above body', async () => {
    const r = await h.req('GET', '/search?q=' + encodeURIComponent('-la'));
    expect(r.json.hits.some((x: { title: string }) => x.title === 'lsdemo')).toBe(true);
    const color = await h.req('GET', '/search?q=' + encodeURIComponent('--color=auto'));
    expect(color.json.hits[0].title).toBe('lsdemo');
    const ranked = (await h.req('GET', '/search?q=lsdemo')).json;
    expect(ranked.hits[0]).toMatchObject({ type: 'commands', title: 'lsdemo' });
    expect(ranked.facets).toMatchObject({ commands: 1, notes: 1 });
  });

  it('supports operators and prefix search', async () => {
    const cmd = (await h.req('GET', '/search?q=' + encodeURIComponent('cmd:lsd'))).json.hits;
    expect(cmd.map((x: { type: string }) => x.type)).toEqual(['commands']);
    const tag = (await h.req('GET', '/search?q=' + encodeURIComponent('tag:linux lsdemo'))).json
      .hits;
    expect(tag).toHaveLength(2);
    const cat = (
      await h.req('GET', '/search?q=' + encodeURIComponent('cat:"file system" type:commands'))
    ).json.hits;
    expect(cat.length).toBeGreaterThan(0);
    expect((await h.req('GET', '/commands?q=lsdemo')).json.items).toHaveLength(1);
  });

  it('accepts the large page size the command library asks for, restricted to one type', async () => {
    const r = await h.req('GET', '/search?q=lsdemo&type=commands&limit=500');
    expect(r.status).toBe(200);
    expect(r.json.hits.map((x: { type: string }) => x.type)).toEqual(['commands']);
    expect((await h.req('GET', '/search?q=x&limit=1001')).status).toBe(400);
  });

  it('stays current after deletes', async () => {
    const id = (await h.req('GET', '/commands?q=lsdemo')).json.items[0].id;
    await h.req('DELETE', `/commands/${id}`);
    expect((await h.req('GET', '/search?q=cmd:lsdemo')).json.hits).toHaveLength(0);
  });
});

describe('taxonomy, templates, settings, backups', () => {
  it('seeds categories and skills, and renames/merges tags', async () => {
    expect(
      (await h.req('GET', '/categories')).json.commands.map((c: { name: string }) => c.name),
    ).toContain('Text Processing');
    expect((await h.req('GET', '/skills')).json).toHaveLength(13);
    const id = (await h.req('POST', '/snippets', { title: 'tagged', tags: ['a', 'b'] })).json.id;
    expect(
      (await h.req('POST', '/tags/rename', { from: 'a', to: 'alpha' })).json.changed,
    ).toBeGreaterThan(0);
    await h.req('POST', '/tags/merge', { from: ['alpha', 'b'], to: 'ab' });
    expect((await h.req('GET', `/snippets/${id}`)).json.tags).toEqual(['ab']);
    expect(
      (await h.req('GET', '/tags')).json.find((t: { name: string }) => t.name === 'ab').count,
    ).toBeGreaterThan(0);
  });

  it('serves default templates matching the real 13-section concept layout', async () => {
    const t = (await h.req('GET', '/templates/concepts')).json;
    expect(t.sections).toHaveLength(13);
    await h.req('PUT', '/templates/concepts', {
      name: 'Mine',
      sections: [{ key: 'a', title: 'A' }],
    });
    expect((await h.req('GET', '/templates/concepts')).json.name).toBe('Mine');
    await h.req('DELETE', '/templates/concepts');
    expect((await h.req('GET', '/templates/concepts')).json.sections).toHaveLength(13);
  });

  it('validates settings strictly', async () => {
    expect((await h.req('PUT', '/settings', { theme: 'neon' })).status).toBe(400);
    const s = (await h.req('PUT', '/settings', { theme: 'light', storeFlagsInLabs: false })).json;
    expect(s.theme).toBe('light');
    expect(s.storeFlagsInLabs).toBe(false);
  });

  it('creates and restores backups', async () => {
    const id = (await h.req('POST', '/snippets', { title: 'before-backup' })).json.id;
    const b = (await h.req('POST', '/backups', { reason: 'unit' })).json;
    await h.req('DELETE', `/snippets/${id}`);
    expect((await h.req('GET', `/snippets/${id}`)).status).toBe(404);
    expect((await h.req('POST', '/backup/restore', { name: b.name })).status).toBe(200);
    expect((await h.req('GET', `/snippets/${id}`)).status).toBe(200);
    expect((await h.req('POST', '/backup/restore', { name: '../../etc.zip' })).status).toBe(400);
  });
});

describe('events', () => {
  it('streams change events over SSE', async () => {
    const res = await fetch(`${h.s.url}/api/v1/events`, {
      headers: { authorization: `Bearer ${h.s.token}` },
    });
    expect(res.status).toBe(200);
    const reader = res.body!.getReader();
    const dec = new TextDecoder();
    let buf = '';
    const created = h.req('POST', '/snippets', { title: 'sse' });
    const deadline = Date.now() + 3000;
    while (!buf.includes('event: change') && Date.now() < deadline) {
      const { value } = await reader.read();
      buf += dec.decode(value);
    }
    await created;
    await reader.cancel();
    expect(buf).toContain('event: hello');
    expect(buf).toContain('event: change');
  });
});
