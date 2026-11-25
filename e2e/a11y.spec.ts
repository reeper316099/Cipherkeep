import { expect, test, type BrowserContext, type Page } from '@playwright/test';
import axe from 'axe-core';
import { mkdtempSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { removeDir } from './cleanup';
import { openApp, startGo, type GoServer } from './go-app';

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

let server: GoServer | undefined;
let context: BrowserContext | undefined;

/** Injects axe into the page and runs the WCAG 2 A and AA rules. */
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
  await context?.close().catch(() => undefined);
  await server?.close().catch(() => undefined);
});

test('starter pack first run, then every page passes axe in dark and light', async ({
  browser,
}) => {
  test.setTimeout(180_000);
  const dataDir = mkdtempSync(path.join(os.tmpdir(), 'ck-a11y-'));
  try {
    server = await startGo(dataDir);
    context = await browser.newContext();
    const page = await openApp(context, server);

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
    await context?.close().catch(() => undefined);
    await server?.close().catch(() => undefined);
    removeDir(dataDir);
  }
});
