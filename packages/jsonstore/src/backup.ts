import AdmZip from 'adm-zip';
import { createWriteStream } from 'node:fs';
import { pipeline } from 'node:stream/promises';
import yazl from 'yazl';
import { promises as fs } from 'node:fs';
import path from 'node:path';
import { atomicWriteFile } from './atomic.ts';

export interface BackupOptions {
  dataDir: string;
  backupDir: string;
  keep?: number;
  /** Top-level names (relative to dataDir) never included in a backup. */
  exclude?: string[];
}

export interface BackupInfo {
  name: string;
  file: string;
  createdAt: string;
  reason: string;
  size: number;
}

const DEFAULT_EXCLUDE = [
  'backups',
  'exports',
  'logs',
  'imports',
  '.quarantine',
  '.lock',
  '.electron-session',
];

/** Rolling zip backups of the whole data directory. */
export class BackupManager {
  readonly keep: number;
  private readonly exclude: Set<string>;
  private readonly mtimes = new Map<string, number>();

  constructor(private readonly opts: BackupOptions) {
    this.keep = opts.keep ?? 20;
    this.exclude = new Set([...DEFAULT_EXCLUDE, ...(opts.exclude ?? [])]);
  }

  /**
   * Streams the data folder into a zip (async deflate, so a big library never blocks the event loop) and
   * moves it into place atomically.
   */
  async create(reason: string): Promise<BackupInfo> {
    const { dataDir, backupDir } = this.opts;
    const files = await this.collect(dataDir, '');
    const stamp = new Date().toISOString().replace(/[-:]/g, '').replace(/\..*$/, '');
    const safe = reason.replace(/[^a-z0-9-]/gi, '-').slice(0, 40);
    let name = `${stamp}-${safe}.zip`;
    for (let n = 1; await exists(path.join(backupDir, name)); n++)
      name = `${stamp}-${safe}-${n}.zip`;
    const file = path.join(backupDir, name);
    await fs.mkdir(backupDir, { recursive: true });
    const tmp = `${file}.tmp`;
    const zip = new yazl.ZipFile();
    for (const f of files) zip.addFile(f.abs, f.entry);
    zip.end();
    try {
      // A file that disappears mid-backup (moved or deleted) raises 'error' on the zip, not on the pipeline;
      // without a listener that is an uncaught exception. Turn it into an ordinary failed backup.
      const zipFailed = new Promise<never>((_, reject) => zip.on('error', reject));
      await Promise.race([pipeline(zip.outputStream, createWriteStream(tmp)), zipFailed]);
      await fs.rename(tmp, file);
    } catch (err) {
      await fs.rm(tmp, { force: true });
      throw err;
    }
    await this.prune();
    return this.info(name);
  }

  /** Create a backup only if the newest one is older than `hours`. */
  async createIfStale(hours = 6, reason = 'launch'): Promise<BackupInfo | null> {
    const [latest] = await this.list();
    if (latest && Date.now() - Date.parse(latest.createdAt) < hours * 3_600_000) return null;
    return this.create(reason);
  }

  async list(): Promise<BackupInfo[]> {
    let names: string[];
    try {
      names = (await fs.readdir(this.opts.backupDir)).filter((n) => n.endsWith('.zip'));
    } catch {
      return [];
    }
    const infos = await Promise.all(names.map((n) => this.info(n)));
    // Newest first. Names alone are not enough: two backups in the same second get a "-1" suffix, which sorts
    // as older, so ties on the timestamp are broken by the file's modification time.
    return infos.sort(
      (a, b) =>
        (a.name.slice(0, 15) < b.name.slice(0, 15)
          ? 1
          : a.name.slice(0, 15) > b.name.slice(0, 15)
            ? -1
            : 0) ||
        (this.mtimes.get(b.name) ?? 0) - (this.mtimes.get(a.name) ?? 0) ||
        (a.name < b.name ? 1 : -1),
    );
  }

  /** Raw bytes of `rel` from the newest backup that contains it, or null. */
  async readFromLatest(rel: string): Promise<Buffer | null> {
    const entry = rel.split(path.sep).join('/');
    for (const b of await this.list()) {
      const found = new AdmZip(b.file).getEntry(entry);
      if (found) return found.getData();
    }
    return null;
  }

  /** Restore a whole backup over the data dir (caller should snapshot first). */
  async restore(name: string): Promise<void> {
    const file = path.join(this.opts.backupDir, path.basename(name));
    const zip = new AdmZip(file);
    for (const e of zip.getEntries()) {
      if (e.isDirectory) continue;
      const target = path.resolve(this.opts.dataDir, e.entryName);
      if (!target.startsWith(path.resolve(this.opts.dataDir) + path.sep)) continue; // zip-slip guard
      await atomicWriteFile(target, e.getData());
    }
  }

  private async prune(): Promise<void> {
    const all = await this.list();
    for (const b of all.slice(this.keep)) await fs.rm(b.file, { force: true });
  }

  private async info(name: string): Promise<BackupInfo> {
    const file = path.join(this.opts.backupDir, name);
    const st = await fs.stat(file);
    const m = /^(\d{4})(\d{2})(\d{2})T(\d{2})(\d{2})(\d{2})-(.*?)(?:-\d+)?\.zip$/.exec(name);
    const createdAt = m
      ? `${m[1]}-${m[2]}-${m[3]}T${m[4]}:${m[5]}:${m[6]}.000Z`
      : st.mtime.toISOString();
    this.mtimes.set(name, st.mtimeMs);
    return { name, file, createdAt, reason: m?.[7] ?? '', size: st.size };
  }

  private async collect(abs: string, rel: string, out: { abs: string; entry: string }[] = []) {
    for (const ent of await fs.readdir(abs, { withFileTypes: true })) {
      if (!rel && this.exclude.has(ent.name)) continue;
      if (ent.name.endsWith('.tmp')) continue;
      const childRel = rel ? `${rel}/${ent.name}` : ent.name;
      const childAbs = path.join(abs, ent.name);
      if (ent.isDirectory()) await this.collect(childAbs, childRel, out);
      else if (ent.isFile()) out.push({ abs: childAbs, entry: childRel });
    }
    return out;
  }
}

async function exists(p: string): Promise<boolean> {
  try {
    await fs.access(p);
    return true;
  } catch {
    return false;
  }
}
