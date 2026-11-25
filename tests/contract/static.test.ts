import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { startTestServer, type TestServer } from './helpers.ts';

let s: TestServer;
let root: string;
let data: string;
beforeAll(async () => {
  root = await mkdtemp(path.join(os.tmpdir(), 'ck-static-'));
  data = await mkdtemp(path.join(os.tmpdir(), 'ck-static-data-'));
  await mkdir(path.join(root, 'assets'));
  await writeFile(path.join(root, 'index.html'), '<!doctype html><title>ck</title>');
  await writeFile(path.join(root, 'assets', 'app-abc123.js'), 'console.log(1)');
  await writeFile(path.join(root, 'assets', 'font.woff2'), 'x');
  await writeFile(path.join(path.dirname(root), 'secret.txt'), 'TOP SECRET');
  s = await startTestServer({ dataDir: data, watch: false, staticDir: root });
});
afterAll(async () => {
  await s.close();
  await rm(root, { recursive: true, force: true });
  await rm(data, { recursive: true, force: true });
  await rm(path.join(path.dirname(root), 'secret.txt'), { force: true });
});

const get = (p: string, init?: RequestInit) => fetch(s.url + p, init);

describe('serving the UI bundle', () => {
  it('serves index and assets without a token, with strict headers', async () => {
    const r = await get('/');
    expect(r.status).toBe(200);
    expect(r.headers.get('content-type')).toMatch(/text\/html/);
    expect(r.headers.get('content-security-policy')).toContain("default-src 'self'");
    expect(r.headers.get('content-security-policy')).toContain("frame-ancestors 'none'");
    expect(r.headers.get('x-content-type-options')).toBe('nosniff');
    expect(await r.text()).toContain('<title>ck</title>');
    const js = await get('/assets/app-abc123.js');
    expect(js.headers.get('content-type')).toMatch(/javascript/);
    expect(js.headers.get('cache-control')).toMatch(/immutable/);
    expect((await get('/assets/font.woff2')).headers.get('content-type')).toBe('font/woff2');
  });

  it('falls back to index.html for client routes but 404s missing assets', async () => {
    expect(await (await get('/commands/abc')).text()).toContain('<title>ck</title>');
    expect((await get('/assets/missing.js')).status).toBe(404);
  });

  it('never reads outside the bundle', async () => {
    for (const p of [
      '/..%2fsecret.txt',
      '/%2e%2e/secret.txt',
      '/assets/..%2f..%2fsecret.txt',
      '/..%5csecret.txt',
      '/%00',
      '/..//..//secret.txt',
    ]) {
      const r = await get(p);
      expect(await r.text(), p).not.toContain('TOP SECRET');
    }
    const raw = await get('/../secret.txt'); // fetch normalises, but check anyway
    expect(await raw.text()).not.toContain('TOP SECRET');
  });

  it('keeps the API locked: token required under /api, none needed for the UI', async () => {
    expect((await get('/api/v1/health')).status).toBe(401);
    expect((await get('/api/v1/nope')).status).toBe(401);
    expect(
      (await get('/api/v1/health', { headers: { authorization: `Bearer ${s.token}` } })).status,
    ).toBe(200);
    expect(
      (await get('/api/v1/health', { headers: { authorization: 'Bearer wrong' } })).status,
    ).toBe(401);
    expect((await get('/', { method: 'POST' })).status).toBe(404); // only GET/HEAD are served
  });

  it('still rejects non-loopback Host headers for the UI', async () => {
    const http = await import('node:http');
    const status = await new Promise<number>((resolve, reject) => {
      const u = new URL(s.url);
      http
        .request(
          { host: u.hostname, port: u.port, path: '/', headers: { host: 'evil.example' } },
          (res) => {
            res.resume();
            resolve(res.statusCode ?? 0);
          },
        )
        .on('error', reject)
        .end();
    });
    expect(status).toBe(403);
  });
});
