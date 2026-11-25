import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { mkdtemp, readFile, readdir, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { startServer, type RunningServer } from '../src/index.ts';
import { blocksToMarkdown, propertyToCell, richTextToMarkdown } from '../src/import/notion-api.ts';
import type { Json } from './helpers.ts';

const TOKEN = 'ntn_FAKE_test_token_0123456789abcdef';
const id = (n: number) => `00000000-0000-4000-8000-${String(n).padStart(12, '0')}`;
const rt = (t: string, a: Json = {}) => [{ plain_text: t, annotations: a, href: null }];
const title = (t: string) => ({ type: 'title', title: rt(t) });
const para = (t: string, extra: Json = {}) => ({
  id: id(900 + t.length),
  type: 'paragraph',
  paragraph: { rich_text: rt(t) },
  ...extra,
});

// A tiny fake Notion workspace.
const DS_CMD = id(1);
const DS_CON = id(2);
const sources = [
  {
    object: 'data_source',
    id: DS_CMD,
    title: rt('Commands'),
    parent: { database_id: id(11) },
    properties: {
      Name: { type: 'title' },
      Category: { type: 'select' },
      Syntax: { type: 'rich_text' },
      Explanation: { type: 'rich_text' },
    },
  },
  {
    object: 'data_source',
    id: DS_CON,
    title: rt('Concepts'),
    parent: { database_id: id(12) },
    properties: { Name: { type: 'title' }, 'Related Commands': { type: 'relation' } },
  },
];
const cmd = (n: number, name: string, syntax: string) => ({
  object: 'page',
  id: id(n),
  parent: { type: 'data_source_id', data_source_id: DS_CMD, database_id: id(11) },
  properties: {
    Name: title(name),
    Category: { type: 'select', select: { name: 'File System' } },
    Syntax: { type: 'rich_text', rich_text: rt(syntax) },
    Explanation: { type: 'rich_text', rich_text: rt(`${name} explained`) },
  },
});
const pages = [
  cmd(21, 'zlist', 'zlist [-a] <dir>'),
  cmd(22, 'zcopy', 'zcopy <src> <dst>'),
  {
    object: 'page',
    id: id(23),
    parent: { type: 'data_source_id', data_source_id: DS_CON },
    properties: {
      Name: title('Zebra Filesystems'),
      'Related Commands': { type: 'relation', relation: [{ id: id(21) }] },
    },
  },
  {
    object: 'page',
    id: id(24),
    parent: { type: 'workspace' },
    properties: { title: title('Loose Guide') },
  },
  {
    object: 'page',
    id: id(25),
    in_trash: true,
    parent: { type: 'workspace' },
    properties: { title: title('Trashed') },
  },
];
const blocks: Record<string, Json[]> = {
  [id(21)]: [
    { id: id(31), type: 'heading_2', heading_2: { rich_text: rt('Usage') } },
    {
      id: id(32),
      type: 'paragraph',
      paragraph: { rich_text: rt('Lists a directory.', { bold: false }) },
    },
    {
      id: id(33),
      type: 'bulleted_list_item',
      has_children: true,
      bulleted_list_item: { rich_text: rt('first') },
    },
    { id: id(34), type: 'code', code: { language: 'bash', rich_text: rt('zlist -a /tmp') } },
    {
      id: id(35),
      type: 'image',
      image: { type: 'file', file: { url: 'https://s3.example/secret.png' }, caption: [] },
    },
  ],
  [id(33)]: [
    { id: id(36), type: 'bulleted_list_item', bulleted_list_item: { rich_text: rt('nested') } },
  ],
  [id(24)]: [para('Hello from the API.')],
};

interface Recorded {
  url: string;
  headers: Record<string, string>;
}
function fakeNotion(opts: { rateLimitOnce?: boolean } = {}) {
  const calls: Recorded[] = [];
  let limited = !opts.rateLimitOnce;
  const fetch = async (url: string, init: RequestInit) => {
    calls.push({ url, headers: init.headers as Record<string, string> });
    if (!limited) {
      limited = true;
      return new Response('{}', { status: 429, headers: { 'retry-after': '0' } });
    }
    const u = new URL(url);
    const list = (results: Json[]) =>
      new Response(JSON.stringify({ results, has_more: false, next_cursor: null }));
    if (u.pathname === '/v1/search') {
      const body = JSON.parse(String(init.body)) as { filter: { value: string } };
      return list(body.filter.value === 'data_source' ? sources : pages);
    }
    const m = /^\/v1\/blocks\/([^/]+)\/children$/.exec(u.pathname);
    if (m) return list(blocks[m[1]!] ?? []);
    return new Response('{}', { status: 404 });
  };
  return { fetch, calls };
}

let dir: string;
let s: RunningServer | undefined;
beforeEach(async () => {
  dir = await mkdtemp(path.join(os.tmpdir(), 'ck-notion-api-'));
});
afterEach(async () => {
  await s?.close();
  s = undefined;
  await rm(dir, { recursive: true, force: true });
});

async function call(method: string, p: string, body?: unknown) {
  const res = await fetch(`${s!.url}/api/v1${p}`, {
    method,
    headers: {
      authorization: `Bearer ${s!.token}`,
      ...(body !== undefined ? { 'content-type': 'application/json' } : {}),
    },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  const text = await res.text();
  return { status: res.status, json: (text ? JSON.parse(text) : null) as Json };
}
async function waitDone(jobId: string) {
  for (let i = 0; i < 200; i++) {
    const r = await call('GET', `/import/notion-api/${jobId}`);
    if (r.json.state !== 'running') return r.json as Json;
    await new Promise((r) => setTimeout(r, 100));
  }
  throw new Error('job did not finish');
}

describe('import from the Notion API', () => {
  it('is off until you turn it on', async () => {
    s = await startServer({
      launchBackupDelayMs: -1,
      dataDir: dir,
      watch: false,
      notionFetch: fakeNotion().fetch,
    });
    const r = await call('POST', '/import/notion-api/start', { token: TOKEN });
    expect(r).toMatchObject({ status: 403, json: { error: { code: 'NOTION_API_DISABLED' } } });
  });

  it('reads the workspace politely, previews like a normal export, imports, and never keeps the token', async () => {
    const fake = fakeNotion({ rateLimitOnce: true });
    s = await startServer({
      launchBackupDelayMs: -1,
      dataDir: dir,
      watch: false,
      notionFetch: fake.fetch,
    });
    await call('PUT', '/settings', {
      ...(await call('GET', '/settings')).json,
      notionApiEnabled: true,
    });

    const start = await call('POST', '/import/notion-api/start', { token: TOKEN });
    expect(start.status).toBe(200);
    const job = await waitDone(start.json.jobId);
    expect(job.state).toBe('done');
    expect(JSON.stringify(job)).not.toContain(TOKEN);
    expect(job.stats).toMatchObject({ databases: 2, pages: 4, images: 1 }); // trashed page skipped

    const p = job.preview;
    expect(p.kind).toBe('notion');
    expect(p.counts).toMatchObject({ commands: 2, concepts: 1 });
    expect(p.items.find((i: Json) => i.title === 'Loose Guide')).toBeTruthy();

    const rep = await call('POST', '/import/commit', {
      previewId: p.previewId,
      strategy: 'update-empty-only',
    });
    expect(rep.json.created).toMatchObject({ commands: 2, concepts: 1 });
    const cmds = (await call('GET', '/commands')).json.items as Json[];
    const zlist = cmds.find((c) => c.name === 'zlist')!;
    expect(zlist.category).toBe('File System');
    expect(zlist.syntax).toBe('zlist [-a] <dir>');
    const concept = ((await call('GET', '/concepts')).json.items as Json[])[0];
    expect(concept.relatedCommandIds).toEqual([zlist.id]);
    expect(((await call('GET', '/notes')).json.items as Json[]).map((n) => n.title)).toContain(
      'Loose Guide',
    );

    // Only api.notion.com, always the pinned version, always the token as a bearer header, and it backed off on 429.
    expect(fake.calls.length).toBeGreaterThan(5);
    for (const c of fake.calls) {
      expect(c.url.startsWith('https://api.notion.com/v1/')).toBe(true);
      expect(c.headers['notion-version']).toBe('2026-03-11');
      expect(c.headers.authorization).toBe(`Bearer ${TOKEN}`);
      expect(c.url).not.toContain(TOKEN);
    }

    // The token is nowhere on disk (data, backups, import records).
    const all: string[] = [];
    const rec = async (d: string) => {
      for (const e of await readdir(d, { withFileTypes: true })) {
        const abs = path.join(d, e.name);
        if (e.isDirectory()) await rec(abs);
        else all.push(abs);
      }
    };
    await rec(dir);
    for (const f of all) expect((await readFile(f)).includes(Buffer.from(TOKEN)), f).toBe(false);
  }, 30_000);

  it('reports a rejected token without leaking it', async () => {
    s = await startServer({
      launchBackupDelayMs: -1,
      dataDir: dir,
      watch: false,
      notionFetch: async () => new Response('{}', { status: 401 }),
    });
    await call('PUT', '/settings', {
      ...(await call('GET', '/settings')).json,
      notionApiEnabled: true,
    });
    const { jobId } = (await call('POST', '/import/notion-api/start', { token: TOKEN })).json;
    const job = await waitDone(jobId);
    expect(job.state).toBe('failed');
    expect(job.message).toMatch(/rejected the token/);
    expect(JSON.stringify(job)).not.toContain(TOKEN);
  });
});

describe('Notion block and property conversion', () => {
  it('renders rich text, lists, code, tables and callouts as Markdown', async () => {
    expect(
      richTextToMarkdown([
        { plain_text: 'bold ', annotations: { bold: true } },
        { plain_text: 'code', annotations: { code: true } },
        { plain_text: ' link', href: 'https://example.com' },
      ]),
    ).toBe('**bold** `code` [link](https://example.com)');
  });

  it('converts a mixed page', async () => {
    const kids: Record<string, Json[]> = {
      t: [
        {
          id: 'r1',
          type: 'table_row',
          table_row: { cells: [[{ plain_text: 'A' }], [{ plain_text: 'B' }]] },
        },
        {
          id: 'r2',
          type: 'table_row',
          table_row: { cells: [[{ plain_text: '1' }], [{ plain_text: '2' }]] },
        },
      ],
    };
    const md = await blocksToMarkdown(
      [
        { id: 'h', type: 'heading_2', heading_2: { rich_text: [{ plain_text: 'Sec' }] } },
        {
          id: 'n1',
          type: 'numbered_list_item',
          numbered_list_item: { rich_text: [{ plain_text: 'one' }] },
        },
        {
          id: 'n2',
          type: 'numbered_list_item',
          numbered_list_item: { rich_text: [{ plain_text: 'two' }] },
        },
        {
          id: 'c',
          type: 'code',
          code: { language: 'plain text', rich_text: [{ plain_text: 'a ``` b' }] },
        },
        {
          id: 'k',
          type: 'callout',
          callout: { icon: { emoji: '💡' }, rich_text: [{ plain_text: 'tip' }] },
        },
        { id: 'td', type: 'to_do', to_do: { checked: true, rich_text: [{ plain_text: 'done' }] } },
        { id: 't', type: 'table', has_children: true, table: {} },
      ],
      async (i) => kids[i] ?? [],
    );
    expect(md).toContain('## Sec');
    expect(md).toContain('1. one\n2. two');
    expect(md).toContain('````\na ``` b\n````');
    expect(md).toContain('> 💡 tip');
    expect(md).toContain('- [x] done');
    expect(md).toContain('| A | B |\n| --- | --- |\n| 1 | 2 |');
  });

  it('writes properties the way Notion exports them', () => {
    const none = () => undefined;
    expect(propertyToCell({ type: 'date', date: { start: '2026-03-24' } }, none)).toBe(
      'March 24, 2026',
    );
    expect(propertyToCell({ type: 'checkbox', checkbox: true }, none)).toBe('Yes');
    expect(
      propertyToCell({ type: 'multi_select', multi_select: [{ name: 'a' }, { name: 'b' }] }, none),
    ).toBe('a, b');
    expect(
      propertyToCell({ type: 'relation', relation: [{ id: id(21) }, { id: id(99) }] }, (x) =>
        x === id(21) ? 'zlist' : undefined,
      ),
    ).toBe('zlist (https://www.notion.so/00000000000040008000000000000021)');
  });
});
