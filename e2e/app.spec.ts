import {
  _electron as electron,
  expect,
  test,
  type ElectronApplication,
  type Page,
} from '@playwright/test';
import AdmZip from 'adm-zip';
import { mkdirSync, mkdtempSync, writeFileSync, existsSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { removeDir } from './cleanup';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const FIXTURE = path.join(ROOT, 'fixtures/synthetic/notion-export');
const MOD = process.platform === 'darwin' ? 'Meta' : 'Control';

const tmp = (p: string) => mkdtempSync(path.join(os.tmpdir(), p));

const launched: ElectronApplication[] = [];
test.afterEach(async () => {
  for (const a of launched.splice(0)) await a.close().catch(() => undefined);
});

async function launch(
  env: Record<string, string>,
): Promise<{ app: ElectronApplication; page: Page }> {
  const bin = process.env.CIPHERKEEP_E2E_BIN; // set to test an installed/packaged build instead of the dev build
  const app = await electron.launch({
    ...(bin ? { executablePath: bin } : {}),
    args: [
      ...(bin ? [] : [path.join(ROOT, 'apps/desktop')]),
      ...(process.env.CI && process.platform === 'linux' ? ['--no-sandbox'] : []),
    ],
    env: { ...(process.env as Record<string, string>), ...env },
  });
  launched.push(app);
  const page = await app.firstWindow();
  await page.waitForLoadState('domcontentloaded');
  return { app, page };
}

test.describe('first run → import → study → persistence', () => {
  const dataDir = tmp('ck-e2e-data-');
  const home = tmp('ck-e2e-home-');
  const env = {
    CIPHERKEEP_DATA_DIR: dataDir,
    HOME: home,
    USERPROFILE: home,
    APPDATA: path.join(home, 'AppData', 'Roaming'),
    XDG_CONFIG_HOME: path.join(home, '.config'),
  };
  test.afterAll(() => {
    removeDir(dataDir);
    removeDir(home);
  });

  test('a whole session survives a restart', async () => {
    let { app, page } = await launch(env);

    // --- security: sandboxed page, token only via the bridge, no remote network ---
    await expect(page.getByText(/Welcome to Cipherkeep/)).toBeVisible();
    const probe = await page.evaluate(async () => {
      const w = window as unknown as {
        cipherkeep?: { token: string };
        require?: unknown;
        process?: unknown;
      };
      const noToken = await fetch('/api/v1/health').then((r) => r.status);
      const withToken = await fetch('/api/v1/health', {
        headers: { authorization: `Bearer ${w.cipherkeep!.token}` },
      }).then((r) => r.status);
      const remote = await fetch('https://example.com/', { mode: 'no-cors' }).then(
        () => 'reached',
        () => 'blocked',
      );
      return {
        hasBridge: typeof w.cipherkeep?.token === 'string' && w.cipherkeep.token.length > 20,
        noRequire: typeof w.require === 'undefined',
        noProcess: typeof w.process === 'undefined',
        noToken,
        withToken,
        remote,
      };
    });
    expect(probe).toEqual({
      hasBridge: true,
      noRequire: true,
      noProcess: true,
      noToken: 401,
      withToken: 200,
      remote: 'blocked',
    });
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

    // --- search a command, fill a placeholder once, copy ---
    await page.getByRole('link', { name: 'Commands' }).first().click();
    await page.getByLabel('Search commands').fill('cp');
    await page.getByRole('option').filter({ hasText: 'cp [source] [destination]' }).first().click();
    const bar = page.getByLabel('Fill in placeholders');
    await bar.getByPlaceholder('<source>').fill('a.txt');
    await bar.getByPlaceholder('<destination>').fill('b.txt');
    await page.getByRole('button', { name: 'Copy syntax' }).click();
    await expect
      .poll(() => app.evaluate(({ clipboard }) => clipboard.readText()))
      .toBe('cp a.txt b.txt');

    // --- command palette: Enter copies the primary example ---
    await page.keyboard.press(`${MOD}+k`);
    await page.getByRole('dialog', { name: 'Command palette' }).getByRole('combobox').fill('ls');
    await expect(page.getByRole('option', { name: /ls/ }).first()).toBeVisible();
    await page.keyboard.press('Enter');
    await expect.poll(() => app.evaluate(({ clipboard }) => clipboard.readText())).toBe('ls -al');

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
    await app.close();
    expect(existsSync(path.join(dataDir, 'meta.json'))).toBe(true);
    expect(existsSync(path.join(dataDir, 'study', 'srs-state.json'))).toBe(true);
    ({ app, page } = await launch(env));
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
    await app.close();
  });
});

test('uses the standard per-OS app data folder when not overridden', async () => {
  // macOS resolves ~/Library from the user database, ignoring $HOME, so this would touch the real profile.
  // The macOS convention is covered by the resolveDataDir unit tests instead.
  test.skip(process.platform === 'darwin', 'would use the real macOS profile');
  const home = tmp('ck-e2e-default-');
  const env: Record<string, string> = {
    HOME: home,
    USERPROFILE: home,
    APPDATA: path.join(home, 'AppData', 'Roaming'),
    XDG_CONFIG_HOME: path.join(home, '.config'),
    CIPHERKEEP_DATA_DIR: '',
  };
  // Windows resolves userData from %APPDATA%, which must exist.
  mkdirSync(env.APPDATA!, { recursive: true });
  mkdirSync(env.XDG_CONFIG_HOME!, { recursive: true });
  const { app, page } = await launch(env);
  const dir = await page.evaluate(
    () => (window as unknown as { cipherkeep: { dataDir: string } }).cipherkeep.dataDir,
  );
  const expected = {
    darwin: path.join(home, 'Library', 'Application Support', 'Cipherkeep'),
    win32: path.join(home, 'AppData', 'Roaming', 'Cipherkeep'),
    linux: path.join(home, '.config', 'Cipherkeep'),
  }[process.platform as 'darwin' | 'win32' | 'linux'];
  expect(path.resolve(dir)).toBe(path.resolve(expected));
  expect(existsSync(path.join(dir, 'meta.json'))).toBe(true);
  await app.close();
  removeDir(home);
});
