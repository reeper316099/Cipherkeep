import { _electron as electron, expect, test, type ElectronApplication } from '@playwright/test';
import { mkdtempSync, readFileSync, readdirSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { removeDir } from './cleanup';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const PASS = 'correct horse battery';
let app: ElectronApplication | undefined;
test.afterEach(async () => {
  await app?.close().catch(() => undefined);
});

const launch = async (dataDir: string) => {
  app = await electron.launch({
    args: [
      path.join(ROOT, 'apps/desktop'),
      ...(process.env.CI && process.platform === 'linux' ? ['--no-sandbox'] : []),
    ],
    env: { ...(process.env as Record<string, string>), CIPHERKEEP_DATA_DIR: dataDir },
  });
  const page = await app.firstWindow();
  await page.waitForLoadState('domcontentloaded');
  return page;
};

test('an encrypted library starts locked, rejects a wrong passphrase, and unlocks', async () => {
  test.setTimeout(120_000);
  const dir = mkdtempSync(path.join(os.tmpdir(), 'ck-e2e-vault-'));
  try {
    let page = await launch(dir);
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
    await app!.close();

    page = await launch(dir);
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
    await app?.close().catch(() => undefined);
    removeDir(dir);
  }
});
