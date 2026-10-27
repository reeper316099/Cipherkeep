import {
  _electron as electron,
  expect,
  test,
  type ElectronApplication,
  type Page,
} from '@playwright/test';
import axe from 'axe-core';
import { mkdtempSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { removeDir } from './cleanup';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const MOD = process.platform === 'darwin' ? 'Meta' : 'Control';
const PAGES = [
  '/',
  '/study',
  '/commands',
  '/concepts',
  '/labs',
  '/projects',
  '/log',
  '/notes',
  '/snippets',
  '/questions',
  '/graph',
  '/settings',
  '/import',
];

let app: ElectronApplication | undefined;

/** AxeBuilder needs a second page, which Electron cannot create; inject axe into the window instead. */
async function audit(page: Page): Promise<string[]> {
  await page.evaluate(axe.source);
  return page.evaluate(async () => {
    const res = await (
      window as unknown as {
        axe: { run: (c: Document, o: unknown) => Promise<{ violations: AxeViolation[] }> };
      }
    ).axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] } });
    return res.violations.map(
      (v) => `${v.id} (${v.impact}): ${v.nodes.map((n) => n.target.join(' ')).join(' | ')}`,
    );
  });
}
interface AxeViolation {
  id: string;
  impact?: string | null;
  nodes: { target: unknown[] }[];
}
test.afterEach(async () => {
  await app?.close().catch(() => undefined);
});

test('starter pack first run, then every page passes axe in dark and light', async () => {
  test.setTimeout(180_000);
  const dataDir = mkdtempSync(path.join(os.tmpdir(), 'ck-a11y-'));
  try {
    app = await electron.launch({
      args: [
        path.join(ROOT, 'apps/desktop'),
        ...(process.env.CI && process.platform === 'linux' ? ['--no-sandbox'] : []),
      ],
      env: { ...(process.env as Record<string, string>), CIPHERKEEP_DATA_DIR: dataDir },
    });
    const page = await app.firstWindow();
    await page.waitForLoadState('domcontentloaded');

    // First run: pick the starter pack and find a bundled command by search.
    await page.getByRole('button', { name: 'Start with the starter pack' }).click();
    await expect(page.getByText(/Loaded \d+ starter items/)).toBeVisible();
    await page.keyboard.press(`${MOD}+k`);
    await page.getByRole('combobox').first().fill('grep');
    await expect(page.getByRole('option').first()).toContainText(/grep/i);
    await page.keyboard.press('Escape');

    // Skip link reaches the main landmark.
    await page.keyboard.press('Tab');
    await expect(page.getByRole('link', { name: 'Skip to main content' })).toBeFocused();

    for (const theme of ['dark', 'light'] as const) {
      await page.evaluate((t) => {
        document.documentElement.dataset.theme = t;
      }, theme);
      for (const route of PAGES) {
        await page.evaluate((r) => {
          window.location.hash = `#${r}`;
        }, route);
        await page.waitForTimeout(400);
        expect(await page.evaluate(() => window.location.hash), route).toBe(`#${route}`);
        await expect(page.locator('#main')).toBeVisible();
        const summary = await audit(page);
        expect(summary, `${theme} ${route}`).toEqual([]);
      }
    }
  } finally {
    await app?.close().catch(() => undefined);
    removeDir(dataDir);
  }
});
