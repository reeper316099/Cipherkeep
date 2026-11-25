import { execFileSync, spawn } from 'node:child_process';
import { mkdtemp, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import readline from 'node:readline';
import { fileURLToPath } from 'node:url';

/**
 * The API contract tests. They were written against the Node server, ran against both servers during the migration
 * (docs/go-migration, phase 4), and now hold the Go server (go/cmd/ck-server) to the same behaviour.
 */

/** A running server under test. */
export interface TestServer {
  url: string;
  token: string;
  close(): Promise<void>;
}

let bin: string | undefined;
/** Builds go/cmd/ck-server once per process (setup.ts builds it once for all files and passes CK_SERVER_BIN). */
export function goBinary(): string {
  bin ??= process.env.CK_SERVER_BIN;
  if (bin) return bin;
  const goDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../go');
  const out = path.join(
    os.tmpdir(),
    `ck-server-test-${process.pid}${process.platform === 'win32' ? '.exe' : ''}`,
  );
  execFileSync('go', ['build', '-o', out, './cmd/ck-server'], { cwd: goDir, stdio: 'inherit' });
  bin = out;
  return out;
}

/** Starts the Go server on a loopback port; it stops cleanly when its stdin closes. */
export async function startTestServer(opts: {
  dataDir: string;
  watch?: boolean;
  staticDir?: string;
  kdf?: { N: number };
}): Promise<TestServer> {
  // No background launch backup in tests: on a slow runner it lands mid-test.
  const args = ['-data', opts.dataDir, `-watch=${opts.watch ?? true}`, '-launch-backup=false'];
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

/** Encrypted files start with this marker, and encrypted log lines with "CKENC1:" (docs/DATA_FORMAT.md). */
const MAGIC = Buffer.from('CKENC1B\0', 'latin1');
export const isEncrypted = (raw: Uint8Array): boolean =>
  raw.length >= MAGIC.length && Buffer.from(raw.subarray(0, MAGIC.length)).equals(MAGIC);
export const isEncryptedLine = (line: string): boolean => line.startsWith('CKENC1:');

// Test responses are untyped JSON; assertions check the shape.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export type Json = any;

export interface Harness {
  s: TestServer;
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
  const s = await startTestServer({ dataDir: d, watch });
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
