import { afterEach, describe, expect, it, vi } from 'vitest';
import { externalHref, installWailsBridge, isWails } from '../src/lib/wails-bridge';

afterEach(() => {
  delete window.cipherkeep;
  delete window.go;
  delete window.runtime;
});

describe('Wails bridge', () => {
  it('recognises the Wails origins only', () => {
    expect(isWails({ protocol: 'wails:', hostname: 'wails' })).toBe(true);
    expect(isWails({ protocol: 'http:', hostname: 'wails.localhost' })).toBe(true);
    expect(isWails({ protocol: 'http:', hostname: '127.0.0.1' })).toBe(false);
  });

  it('sends only foreign web and mail links to the system browser', () => {
    const link = (href: string) => {
      const a = document.createElement('a');
      a.href = href;
      const span = document.createElement('span');
      a.appendChild(span);
      return span;
    };
    const origin = 'wails://wails';
    expect(externalHref(link('https://example.com/x'), origin)).toBe('https://example.com/x');
    expect(externalHref(link('mailto:a@example.com'), origin)).toBe('mailto:a@example.com');
    expect(externalHref(link('javascript:alert(1)'), origin)).toBeUndefined();
    expect(externalHref(link('file:///etc/passwd'), origin)).toBeUndefined();
    expect(externalHref(document.createElement('div'), origin)).toBeUndefined();
  });

  it('builds window.cipherkeep from the Go bindings, with no token', async () => {
    const off = vi.fn();
    const handlers: Record<string, (d: unknown) => void> = {};
    const app = {
      Boot: vi.fn().mockResolvedValue({ platform: 'darwin', version: '1.2.3', dataDir: '/data' }),
      OpenExternal: vi.fn().mockResolvedValue(true),
      RevealInFolder: vi.fn().mockResolvedValue(true),
      OpenDataFolder: vi.fn().mockResolvedValue(undefined),
      PickFolder: vi.fn().mockResolvedValue(''),
      SetDataFolder: vi.fn().mockResolvedValue({ ok: true }),
    };
    window.go = { main: { App: app } };
    window.runtime = {
      EventsOn: (name, cb) => {
        handlers[name] = cb as (d: unknown) => void;
        return off;
      },
    };
    await installWailsBridge({ protocol: 'wails:', hostname: 'wails' });
    const b = window.cipherkeep!;
    expect(b).toMatchObject({ apiBase: '', token: '', platform: 'darwin', dataDir: '/data' });
    expect(await b.pickFolder!()).toBeNull(); // a cancelled dialog
    const got: string[] = [];
    const stop = b.onEvent!((k) => got.push(k));
    b.onMenu!((a) => got.push(a));
    handlers['ck:event']!('change');
    handlers['ck:menu']!('nav:/study');
    expect(got).toEqual(['change', 'nav:/study']);
    stop();
    expect(off).toHaveBeenCalled();
    window.open('https://example.com');
    expect(app.OpenExternal).toHaveBeenCalledWith('https://example.com');
  });

  it('does nothing outside Wails (Electron and the dev browser keep their own setup)', async () => {
    await installWailsBridge({ protocol: 'http:', hostname: '127.0.0.1' });
    expect(window.cipherkeep).toBeUndefined();
  });
});
