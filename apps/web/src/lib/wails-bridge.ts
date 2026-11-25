// In the Go + Wails desktop app there is no preload script. This builds the same `window.cipherkeep` surface the
// Electron preload offers, from the Go bindings (window.go.main.App) and the Wails runtime (events). The API itself
// needs no bridge: fetch('/api/v1/...') is answered inside the process, so there is no address and no token.

interface WailsApp {
  Boot(): Promise<{ platform: string; version: string; dataDir: string }>;
  OpenExternal(url: string): Promise<boolean>;
  RevealInFolder(path: string): Promise<boolean>;
  OpenDataFolder(): Promise<void>;
  PickFolder(): Promise<string>;
  SetDataFolder(target: string, copy: boolean): Promise<{ ok: boolean; reason?: string }>;
}

declare global {
  interface Window {
    go?: { main?: { App?: WailsApp } };
    runtime?: { EventsOn(name: string, cb: (...data: unknown[]) => void): () => void };
  }
}

/** Wails serves the UI from wails://wails (macOS, Linux) or http://wails.localhost (Windows). */
export function isWails(loc: Pick<Location, 'protocol' | 'hostname'> = window.location): boolean {
  return loc.protocol === 'wails:' || loc.hostname === 'wails.localhost';
}

async function bindings(): Promise<WailsApp | undefined> {
  for (let i = 0; i < 40; i++) {
    const app = window.go?.main?.App;
    if (app && window.runtime) return app;
    await new Promise((r) => setTimeout(r, 25));
  }
  return undefined;
}

/** Only web and mail links leave the app, and they open in the system browser, never in this window. */
export function externalHref(el: Element | null, origin: string): string | undefined {
  const a = el?.closest?.('a[href]') as HTMLAnchorElement | null | undefined;
  if (!a) return undefined;
  try {
    const u = new URL(a.href);
    if (!['http:', 'https:', 'mailto:'].includes(u.protocol) || u.origin === origin)
      return undefined;
    return u.href;
  } catch {
    return undefined;
  }
}

export async function installWailsBridge(
  loc: Pick<Location, 'protocol' | 'hostname'> = window.location,
): Promise<void> {
  if (window.cipherkeep || !isWails(loc)) return;
  const app = await bindings();
  const rt = window.runtime;
  if (!app || !rt) return;
  const boot = await app.Boot();
  window.cipherkeep = {
    apiBase: '',
    token: '',
    platform: boot.platform,
    version: boot.version,
    dataDir: boot.dataDir,
    openExternal: (url) => app.OpenExternal(url),
    revealInFolder: (p) => app.RevealInFolder(p),
    openDataFolder: () => app.OpenDataFolder(),
    pickFolder: async () => (await app.PickFolder()) || null,
    setDataFolder: (target, copy) => app.SetDataFolder(target, copy),
    onMenu: (cb) => rt.EventsOn('ck:menu', (action) => cb(String(action))),
    onEvent: (cb) => rt.EventsOn('ck:event', (kind) => cb(String(kind))),
  };
  document.addEventListener(
    'click',
    (e) => {
      const href = externalHref(e.target as Element | null, window.location.origin);
      if (!href) return;
      e.preventDefault();
      void app.OpenExternal(href);
    },
    true,
  );
  window.open = (url?: string | URL) => {
    if (url) void app.OpenExternal(String(url));
    return null;
  };
}
