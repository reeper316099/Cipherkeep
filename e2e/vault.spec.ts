import { expect, test, type BrowserContext } from '@playwright/test';
import { mkdtempSync, readFileSync, readdirSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { removeDir } from './cleanup';
import { openApp, startGo, type GoServer } from './go-app';

const PASS = 'correct horse battery';
let server: GoServer | undefined;
let context: BrowserContext | undefined;
const quit = async () => {
  await context?.close().catch(() => undefined);
  await server?.close().catch(() => undefined);
  context = server = undefined;
};
test.afterEach(quit);

test('an encrypted library starts locked, rejects a wrong passphrase, and unlocks', async ({
  browser,
}) => {
  test.setTimeout(120_000);
  const dir = mkdtempSync(path.join(os.tmpdir(), 'ck-e2e-vault-'));
  try {
    server = await startGo(dir);
    context = await browser.newContext();
    let page = await openApp(context, server);
    await expect(page.getByText(/Welcome to Cipherkeep/)).toBeVisible();
    await page.evaluate(async (pass) => {
      const b = (window as unknown as { cipherkeep: { apiBase: string; token: string } })
        .cipherkeep;
      const h = { authorization: `Bearer ${b.token}`, 'content-type': 'application/json' };
      await fetch(`${b.apiBase}/api/v1/notes`, {
        method: 'POST',
        headers: h,
        body: JSON.stringify({ title: 'Zebra e2e note', markdown: 'ZEBRA-E2E-BODY' }),
      });
      const r = await fetch(`${b.apiBase}/api/v1/vault/enable`, {
        method: 'POST',
        headers: h,
        body: JSON.stringify({ passphrase: pass }),
      });
      if (!r.ok) throw new Error(String(r.status));
    }, PASS);
    const notes = path.join(dir, 'data/notes');
    expect(readdirSync(notes)).toEqual(['_enc']);
    for (const f of readdirSync(path.join(notes, '_enc')))
      expect(readFileSync(path.join(notes, '_enc', f)).includes('ZEBRA-E2E-BODY')).toBe(false);
    await quit();

    server = await startGo(dir);
    context = await browser.newContext();
    page = await openApp(context, server);
    await expect(page.getByRole('heading', { name: /is locked/i })).toBeVisible();
    await page.getByLabel('PASSPHRASE').fill('not the passphrase');
    await page.getByRole('button', { name: 'Unlock' }).click();
    await expect(page.getByRole('alert')).toContainText('not right');
    await page.getByLabel('PASSPHRASE').fill(PASS);
    await page.getByRole('button', { name: 'Unlock' }).click();
    await expect(page.getByRole('heading', { name: /is locked/i })).toBeHidden();
    await page.evaluate(() => (window.location.hash = '#/notes'));
    await expect(page.getByText('Zebra e2e note').first()).toBeVisible();
  } finally {
    await quit();
    removeDir(dir);
  }
});
