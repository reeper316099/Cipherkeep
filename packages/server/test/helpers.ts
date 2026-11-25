import { execFileSync, spawn } from 'node:child_process';
import { mkdtemp, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import readline from 'node:readline';
import { fileURLToPath } from 'node:url';
import { startServer, type RunningServer } from '../src/index.ts';

/**
 * CK_SERVER=go runs these tests against the Go server (go/cmd/ck-server) instead of the Node one, so both versions
 * answer the same contract (`npm run test:go-contract`). Tests that reach into Node internals (`s.ctx`) only run
 * against Node.
 */
export const GO = process.env.CK_SERVER === 'go';

/** What the tests need from a running server, Node or Go. */
export interface TestServer {
  url: string;
  token: string;
  close(): Promise<void>;
}

let goBin: string | undefined;
/** Builds go/cmd/ck-server once per process. The contract config builds it once for all files (CK_SERVER_BIN). */
export function goBinary(): string {
  goBin ??= process.env.CK_SERVER_BIN;
  if (goBin) return goBin;
  const goDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../../go');
  const out = path.join(
    os.tmpdir(),
    `ck-server-test-${process.pid}${process.platform === 'win32' ? '.exe' : ''}`,
  );
  execFileSync('go', ['build', '-o', out, './cmd/ck-server'], { cwd: goDir, stdio: 'inherit' });
  goBin = out;
  return out;
}

/** Starts the Go server on a loopback port; it stops cleanly when its stdin closes. */
async function startGoServer(opts: {
  dataDir: string;
  watch?: boolean;
  staticDir?: string;
  kdf?: { N: number };
}): Promise<TestServer> {
  const args = ['-data', opts.dataDir, `-watch=${opts.watch ?? true}`];
  if (opts.staticDir) args.push('-static', opts.staticDir);
  if (opts.kdf) args.push('-kdf-n', String(opts.kdf.N));
  const child = spawn(goBinary(), args, { stdio: ['pipe', 'pipe', 'inherit'] });
  const exited = new Promise<void>((resolve) => child.once('exit', () => resolve()));
  const line = await new Promise<string>((resolve, reject) => {
    const rl = readline.createInterface({ input: child.stdout! });
    rl.once('line', (l) => {
      rl.close();
      resolve(l);
    });
    child.once('exit', (code) => reject(new Error(`ck-server exited with ${code}`)));
  });
  const info = JSON.parse(line) as { url?: string; token?: string; error?: string };
  if (!info.url)
    throw Object.assign(new Error(info.error ?? 'ck-server failed'), { code: 'LOCKED' });
  return {
    url: info.url,
    token: info.token!,
    async close() {
      child.stdin!.end();
      await exited;
    },
  };
}

/** Starts the server under test: Node by default, Go when CK_SERVER=go. */
export async function startTestServer(opts: {
  dataDir: string;
  watch?: boolean;
  staticDir?: string;
  kdf?: { algo: 'scrypt'; N: number; r: number; p: number };
}): Promise<TestServer> {
  return GO ? startGoServer(opts) : startServer(opts);
}

// Test responses are untyped JSON; assertions check the shape.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export type Json = any;

export interface Harness {
  s: RunningServer;
  dir: string;
  req(
    method: string,
    p: string,
    body?: unknown,
    token?: string | null,
  ): Promise<{ status: number; json: Json; headers: Headers }>;
  stop(): Promise<void>;
}

export async function harness(watch = false, dir?: string): Promise<Harness> {
  const d = dir ?? (await mkdtemp(path.join(os.tmpdir(), 'ck-server-')));
  // The Go server only offers what TestServer describes; tests that use `s.ctx` are Node-only.
  const s = (await startTestServer({ dataDir: d, watch })) as RunningServer;
  return {
    s,
    dir: d,
    async req(method, p, body, token) {
      const t = token === undefined ? s.token : token;
      const res = await fetch(`${s.url}/api/v1${p}`, {
        method,
        headers: {
          ...(t ? { authorization: `Bearer ${t}` } : {}),
          ...(body !== undefined ? { 'content-type': 'application/json' } : {}),
        },
        body: body !== undefined ? JSON.stringify(body) : undefined,
      });
      const text = await res.text();
      return { status: res.status, json: text ? JSON.parse(text) : null, headers: res.headers };
    },
    async stop() {
      await s.close();
      if (!dir) await rm(d, { recursive: true, force: true });
    },
  };
}

export const SAMPLES: Record<string, Record<string, unknown>> = {
  commands: { name: 'ls', category: 'File System', syntax: 'ls [flags] [directory]' },
  concepts: { title: 'Linux File System', category: 'Linux' },
  labs: { title: 'Bandit 0', platform: 'Bandit' },
  projects: { title: 'Printer Control', type: 'Script' },
  log: { title: 'Day one', date: '2026-03-24' },
  notes: { title: 'SSH Guide', folder: 'Guides', markdown: '# SSH\n' },
  snippets: { title: 'one-liner', language: 'bash', code: 'echo hi' },
};
