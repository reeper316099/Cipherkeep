import type { BrowserContext, Page } from '@playwright/test';
import { spawn } from 'node:child_process';
import readline from 'node:readline';

/** A running Go API server serving the desktop app's UI bundle on a loopback port. */
export interface GoServer {
  url: string;
  token: string;
  dataDir: string;
  close(): Promise<void>;
}

/**
 * Starts go/cmd/ck-server on a data folder. The desktop app runs the same API handler in process; over loopback the
 * server adds a per-launch token, which the page gets the way the desktop bridge gives it the rest (window.cipherkeep).
 */
export async function startGo(dataDir: string): Promise<GoServer> {
  const bin = process.env.CK_E2E_SERVER;
  const ui = process.env.CK_E2E_UI;
  if (!bin || !ui)
    throw new Error('run through playwright.config.ts (global setup builds the server and UI)');
  const child = spawn(bin, ['-data', dataDir, '-static', ui, '-launch-backup=false'], {
    stdio: ['pipe', 'pipe', 'inherit'],
  });
  const exited = new Promise<void>((r) => child.once('exit', () => r()));
  const line = await new Promise<string>((resolve, reject) => {
    const rl = readline.createInterface({ input: child.stdout! });
    rl.once('line', (l) => {
      rl.close();
      resolve(l);
    });
    child.once('exit', (code) => reject(new Error(`ck-server exited with ${code}`)));
  });
  const info = JSON.parse(line) as { url?: string; token?: string; error?: string };
  if (!info.url) throw new Error(info.error ?? 'ck-server failed');
  return {
    url: info.url,
    token: info.token!,
    dataDir,
    async close() {
      child.stdin!.end(); // a clean shutdown: the library lock is released
      await exited;
    },
  };
}

/**
 * Opens the app UI in a page. The init script plays the desktop bridge's part (window.cipherkeep with the token and the
 * data folder) and records what the app copies, since browsers keep the real clipboard behind permissions.
 */
export async function openApp(context: BrowserContext, s: GoServer): Promise<Page> {
  await context.addInitScript(
    ({ token, dataDir }) => {
      const w = window as unknown as {
        cipherkeep: unknown;
        __copied: string[];
      };
      w.cipherkeep = { apiBase: '', token, platform: 'e2e', version: 'e2e', dataDir };
      w.__copied = [];
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: {
          writeText: async (t: string) => void w.__copied.push(t),
          readText: async () => w.__copied.at(-1) ?? '',
        },
      });
    },
    { token: s.token, dataDir: s.dataDir },
  );
  const page = await context.newPage();
  await page.goto(s.url + '/');
  await page.waitForLoadState('domcontentloaded');
  return page;
}

/** The last text the app copied. */
export const copied = (page: Page): Promise<string> =>
  page.evaluate(() => (window as unknown as { __copied: string[] }).__copied.at(-1) ?? '');
