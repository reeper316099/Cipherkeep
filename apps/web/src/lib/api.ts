import type { Command, Concept, Lab } from '@cipherkeep/shared';

declare global {
  interface Window {
    /**
     * Injected by the Electron preload via contextBridge, or built from the Wails bindings (wails-bridge.ts). Absent in
     * the dev browser (Vite proxies /api). In the Wails app apiBase and token are empty: the API is in process.
     */
    cipherkeep?: {
      apiBase: string;
      token: string;
      platform?: string;
      version?: string;
      dataDir?: string;
      openExternal?: (url: string) => Promise<boolean>;
      revealInFolder?: (path: string) => Promise<boolean>;
      openDataFolder?: () => Promise<void>;
      pickFolder?: () => Promise<string | null>;
      setDataFolder?: (target: string, copy: boolean) => Promise<{ ok: boolean; reason?: string }>;
      onMenu?: (cb: (action: string) => void) => () => void;
      /** Wails only: live library events, in place of the server-sent event stream. */
      onEvent?: (cb: (kind: string) => void) => () => void;
    };
  }
}

/** Fired when the server answers 423: the library was locked (or is still locked) and needs a passphrase. */
export const LOCKED_EVENT = 'ck-locked';

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
    readonly requestId?: string,
    readonly details?: unknown,
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

function base(): { url: string; headers: Record<string, string> } {
  const b = window.cipherkeep;
  return b
    ? {
        url: `${b.apiBase}/api/v1`,
        headers: b.token ? { authorization: `Bearer ${b.token}` } : {},
      }
    : { url: '/api/v1', headers: {} };
}

export async function api<T>(
  path: string,
  init: RequestInit & { json?: unknown } = {},
): Promise<T> {
  const { url, headers } = base();
  const h: Record<string, string> = {
    ...headers,
    ...(init.headers as Record<string, string> | undefined),
  };
  let body = init.body;
  if (init.json !== undefined) {
    h['content-type'] = 'application/json';
    body = JSON.stringify(init.json);
  }
  const res = await fetch(url + path, { ...init, headers: h, body });
  if (res.status === 423) window.dispatchEvent(new Event(LOCKED_EVENT));
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  const data = text ? (JSON.parse(text) as unknown) : null;
  if (!res.ok) {
    const e = (
      data as {
        error?: { code: string; message: string; requestId?: string; details?: unknown };
      } | null
    )?.error;
    throw new ApiError(
      res.status,
      e?.code ?? 'ERROR',
      e?.message ?? res.statusText,
      e?.requestId,
      e?.details,
    );
  }
  return data as T;
}

/** Fetch a binary resource (attachments) through the authenticated API. */
export async function apiBlob(path: string): Promise<Blob> {
  const { url, headers } = base();
  const res = await fetch(url + path, { headers });
  if (!res.ok) throw new ApiError(res.status, 'ERROR', res.statusText);
  return res.blob();
}

/** Download an export through the authenticated API and hand it to the browser/OS save flow. */
export async function downloadExport(path: string, fallbackName: string): Promise<void> {
  const { url, headers } = base();
  const res = await fetch(url + path, { headers });
  if (!res.ok) throw new ApiError(res.status, 'ERROR', res.statusText);
  const name =
    /filename="([^"]+)"/.exec(res.headers.get('content-disposition') ?? '')?.[1] ?? fallbackName;
  const a = document.createElement('a');
  a.href = URL.createObjectURL(await res.blob());
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(a.href), 10_000);
}

export interface ListResponse<T> {
  items: T[];
  total: number;
  limit: number;
  offset: number;
}

/** Every entity in a collection (pages of 500). `omit` leaves bulky fields out of each item. */
export async function listAll<T>(collection: string, omit?: string[]): Promise<T[]> {
  const out: T[] = [];
  const skip = omit?.length ? `&omit=${omit.join(',')}` : '';
  for (let offset = 0; ; offset += 500) {
    const page = await api<ListResponse<T>>(
      `/${collection}?limit=500&offset=${offset}&archived=all&sort=title${skip}`,
    );
    out.push(...page.items);
    if (out.length >= page.total || page.items.length === 0) return out;
  }
}

export type CommandEntity = Command;
export type ConceptEntity = Concept;
export type LabEntity = Lab;

export interface SearchHit {
  type: string;
  id: string;
  title: string;
  category: string;
  score: number;
  highlight: string;
}
export interface SearchResponse {
  hits: SearchHit[];
  facets: Record<string, number>;
}

export interface Settings {
  theme: 'dark' | 'light' | 'system';
  accent: string;
  density: 'comfortable' | 'compact';
  storeFlagsInLabs: boolean;
  blurSensitive: boolean;
  notionApiEnabled: boolean;
  vimMode: boolean;
  backupKeep: number;
  backupIntervalHours: number;
  sanitizePatterns: string[];
  studyNewPerDay: number;
}

export interface Meta {
  appName: string;
  collections: string[];
  counts: Record<string, number>;
}
