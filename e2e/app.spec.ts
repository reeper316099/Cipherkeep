import { expect, test, type Browser, type BrowserContext, type Page } from '@playwright/test';
import AdmZip from 'adm-zip';
import { existsSync, mkdtempSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { removeDir } from './cleanup';
import { copied, openApp, startGo, type GoServer } from './go-app';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const FIXTURE = path.join(ROOT, 'fixtures/synthetic/notion-export');
const MOD = process.platform === 'darwin' ? 'Meta' : 'Control';
const tmp = (p: string) => mkdtempSync(path.join(os.tmpdir(), p));

let server: GoServer | undefined;
let context: BrowserContext | undefined;
test.afterEach(async () => {
  await context?.close().catch(() => undefined);
  await server?.close().catch(() => undefined);
  context = server = undefined;
});

async function launch(browser: Browser, dataDir: string): Promise<Page> {
  server = await startGo(dataDir);
  context = await browser.newContext();
  return openApp(context, server);
}
async function quit(): Promise<void> {
  await context?.close();
  await server?.close();
  context = server = undefined;
}

test('a whole session survives a restart', async ({ browser }) => {
  const dataDir = tmp('ck-e2e-data-');
  try {
    let page = await launch(browser, dataDir);

    // --- security: the API needs the per-launch token, and the page cannot reach the network ---
    await expect(page.getByText(/Welcome to Cipherkeep/)).toBeVisible();
    const probe = await page.evaluate(async () => {
      const w = window as unknown as { cipherkeep: { token: string } };
      const noToken = await fetch('/api/v1/health').then((r) => r.status);
      const withToken = await fetch('/api/v1/health', {
        headers: { authorization: `Bearer ${w.cipherkeep.token}` },
      }).then((r) => r.status);
      const remote = await fetch('https://example.com/', { mode: 'no-cors' }).then(
        () => 'reached',
        () => 'blocked',
      );
      return { noToken, withToken, remote };
    });
    expect(probe).toEqual({ noToken: 401, withToken: 200, remote: 'blocked' });
    expect(page.url()).toMatch(/^http:\/\/127\.0\.0\.1:\d+\//);

    // --- import the synthetic Notion export through the UI ---
    const zip = new AdmZip();
    zip.addLocalFolder(FIXTURE);
    const zipPath = path.join(tmp('ck-e2e-zip-'), 'export.zip');
    writeFileSync(zipPath, zip.toBuffer());
    await page.getByRole('link', { name: 'Import and export' }).click();
    await page.getByLabel('Choose file to import').setInputFiles(zipPath);
    await expect(page.getByRole('region', { name: 'Import preview' })).toContainText('15');
    await page.getByRole('button', { name: 'Import', exact: true }).click();
    await expect(page.getByRole('region', { name: 'Import report' })).toContainText('created');
    removeDir(path.dirname(zipPath));

    // --- search a command, fill a placeholder once, copy ---
    await page.getByRole('link', { name: 'Commands' }).first().click();
    await page.getByLabel('Search commands').fill('cp');
    await page.getByRole('option').filter({ hasText: 'cp [source] [destination]' }).first().click();
    const bar = page.getByLabel('Fill in placeholders');
    await bar.getByPlaceholder('<source>').fill('a.txt');
    await bar.getByPlaceholder('<destination>').fill('b.txt');
    await page.getByRole('button', { name: 'Copy syntax' }).click();
    await expect.poll(() => copied(page)).toBe('cp a.txt b.txt');

    // --- command palette: Enter copies the primary example ---
    await page.keyboard.press(`${MOD}+k`);
    await page.getByRole('dialog', { name: 'Command palette' }).getByRole('combobox').fill('ls');
    await expect(page.getByRole('option', { name: /ls/ }).first()).toBeVisible();
    await page.keyboard.press('Enter');
    await expect.poll(() => copied(page)).toBe('ls -al');

    // --- study five cards from the keyboard ---
    await page.getByRole('link', { name: 'Review' }).click();
    await page.getByRole('button', { name: /Start review/ }).click();
    for (let i = 0; i < 5; i++) {
      const typed = page.getByLabel('Your answer');
      await expect(page.getByLabel('Flashcard')).toBeVisible();
      if (await typed.count()) {
        await page.keyboard.press('Enter'); // empty answer: checks as wrong
      } else {
        await page.keyboard.press('Space');
      }
      await expect(page.getByRole('group', { name: 'How well did you know it?' })).toBeVisible();
      await page.keyboard.press('3');
      // wait until this answer is saved and the next card is on screen (counter reads "n/total")
      await expect(page.getByText(new RegExp(`^${i + 1}/\\d+ ·`))).toBeVisible();
    }
    await page.keyboard.press('Escape');
    await expect(page.getByText('Session complete')).toBeVisible();

    // --- log a lab ---
    await page.getByRole('link', { name: 'Labs' }).click();
    await page.getByRole('button', { name: '+ New lab' }).click();
    await page.getByRole('dialog', { name: 'New lab' }).getByRole('textbox').fill('E2E Bandit 0');
    await page.getByRole('button', { name: 'Create' }).click();
    const editor = page.getByRole('dialog', { name: 'E2E Bandit 0' });
    await editor.getByRole('button', { name: 'Completed' }).click();
    await editor.getByRole('button', { name: /^Save/ }).click();
    await expect(page.getByText('Saved')).toBeVisible();
    await page.keyboard.press('Escape');

    // --- the dashboard reflects it ---
    await page.getByRole('link', { name: 'Home' }).click();
    await expect(page.getByLabel('Status banner')).toContainText('THREAT LEVEL: LOW');
    await expect(page.getByText('Labs completed')).toBeVisible();

    // --- restart: nothing is lost ---
    await quit();
    expect(existsSync(path.join(dataDir, 'meta.json'))).toBe(true);
    expect(existsSync(path.join(dataDir, 'study', 'srs-state.json'))).toBe(true);
    expect(existsSync(path.join(dataDir, '.lock'))).toBe(false); // released on a clean quit
    page = await launch(browser, dataDir);
    await expect(page.getByLabel('Status banner')).toContainText('STATUS: ACTIVE');
    await expect(page.getByRole('link', { name: /Commands/ }).first()).toContainText('15');
    await page.getByRole('link', { name: 'Labs', exact: true }).click();
    await expect(page.getByRole('button', { name: /^E2E Bandit 0/ })).toBeVisible(); // the board card
    await page.getByRole('link', { name: 'Review', exact: true }).click();
    await expect(page.getByText('Reviewed today')).toBeVisible();
    const stats = await page.evaluate(async () => {
      const b = (window as unknown as { cipherkeep: { token: string } }).cipherkeep;
      return fetch('/api/v1/study/stats', { headers: { authorization: `Bearer ${b.token}` } }).then(
        (r) => r.json(),
      );
    });
    expect(stats.reviewedToday).toBeGreaterThanOrEqual(5);
  } finally {
    await quit().catch(() => undefined);
    removeDir(dataDir);
  }
});

test('a hand edit on disk reaches the open page', async ({ browser }) => {
  const dataDir = tmp('ck-e2e-live-');
  try {
    const page = await launch(browser, dataDir);
    await expect(page.getByText(/Welcome to Cipherkeep/)).toBeVisible();
    await page.evaluate(() => (window.location.hash = '#/notes'));
    writeFileSync(
      path.join(dataDir, 'data/notes/written-by-hand.md'),
      '---\nid: 01JABCDEFGHJKMNPQRSTVWXYZ0\ntitle: Written by hand\ncreatedAt: 2026-01-01T00:00:00.000Z\nupdatedAt: 2026-01-01T00:00:00.000Z\n---\nfrom another editor\n',
    );
    await expect(page.getByText('Written by hand').first()).toBeVisible();
  } finally {
    await quit().catch(() => undefined);
    removeDir(dataDir);
  }
});
