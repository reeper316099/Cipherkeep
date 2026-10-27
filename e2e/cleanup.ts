import { rmSync } from 'node:fs';

/** Windows keeps Electron's cache files locked for a moment after the app exits; cleanup is best effort. */
export function removeDir(dir: string): void {
  try {
    rmSync(dir, { recursive: true, force: true, maxRetries: 20, retryDelay: 250 });
  } catch {
    /* a leftover temp folder must never fail a test */
  }
}
