import { randomBytes, timingSafeEqual } from 'node:crypto';
import { promises as fs } from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import type { FastifyInstance } from 'fastify';
import {
  AesGcmCodec,
  CodecHolder,
  PlainCodec,
  acquireInstanceLock,
  type InstanceLock,
} from '@cipherkeep/jsonstore';
import { buildApp, buildLockedApp } from './app.ts';
import type { FetchFn } from './import/notion-api.ts';
import { createContext, type Context } from './context.ts';
import { normalizeEncryption, recodeLibrary } from './encryption.ts';
import { AttemptLimiter, Vault, type KdfParams } from './vault.ts';
import type { VaultControls } from './vault-routes.ts';
import { AppError } from './util.ts';

export * from './config.ts';
export * from './context.ts';
export * from './app.ts';
export { tokenize, parseQuery, SearchIndex } from './search.ts';
export { Vault, generateRecoveryKey, normalizeRecoveryKey } from './vault.ts';

export interface RunningServer {
  url: string;
  token: string;
  /** The open library. Throws while an encrypted library is locked. */
  readonly ctx: Context;
  readonly locked: boolean;
  close(): Promise<void>;
}

type Handler = (req: http.IncomingMessage, res: http.ServerResponse) => void;

/**
 * Start the API on 127.0.0.1 with an ephemeral port and a per-launch bearer token.
 *
 * One listening socket serves either the full API (library open) or a small "locked" app (encrypted library,
 * no key yet). Unlocking builds the full app and swaps it in; locking swaps back and drops the key.
 */
export async function startServer(opts: {
  dataDir: string;
  token?: string;
  port?: number;
  watch?: boolean;
  logger?: boolean;
  /** Built web bundle to serve (desktop app). */
  staticDir?: string;
  /** scrypt cost for new vaults (tests lower it). */
  kdf?: KdfParams;
  /** Test hook: replaces the network for the optional Notion API import. */
  notionFetch?: FetchFn;
  /** When the background "launch" backup runs (default 3000 ms; negative: never, as tests use). */
  launchBackupDelayMs?: number;
}): Promise<RunningServer> {
  const token = opts.token ?? randomBytes(32).toString('base64url');
  const dataDir = path.resolve(opts.dataDir);
  await fs.mkdir(dataDir, { recursive: true });
  const lock: InstanceLock = await acquireInstanceLock(dataDir);
  const vault = new Vault(dataDir, opts.kdf);
  const limiter = new AttemptLimiter();

  let ctx: Context | undefined;
  let openApp: FastifyInstance | undefined;
  let lockedApp: FastifyInstance | undefined;
  let current: Handler | undefined;
  let busy = false;
  let key: Buffer | undefined; // the master key, only while the library is open and encrypted

  const handlers = new Map<FastifyInstance, Handler>();
  const factory = (holder: { h?: Handler }) => (handler: Handler) => {
    holder.h = handler;
    return http.createServer(handler);
  };
  const track = async (app: Promise<FastifyInstance>, holder: { h?: Handler }) => {
    const a = await app;
    await a.ready();
    handlers.set(a, holder.h!);
    return a;
  };

  const server = http.createServer((req, res) => {
    if (!current) {
      res.writeHead(503).end();
      return;
    }
    current(req, res);
  });

  const requireOpen = (): Context => {
    if (!ctx) throw new AppError(423, 'LOCKED', 'Unlock your library to continue');
    return ctx;
  };

  const verify = async (passphrase: string): Promise<void> => {
    limiter.check();
    try {
      const k = await vault.unlockWithPassphrase(passphrase);
      if (!key || !timingSafeEqual(k, key))
        throw new AppError(401, 'BAD_CREDENTIALS', 'Wrong passphrase');
      limiter.ok();
    } catch (e) {
      if (e instanceof AppError && e.code === 'BAD_CREDENTIALS') limiter.fail();
      throw e;
    }
  };

  const exclusive = async <T>(fn: () => Promise<T>): Promise<T> => {
    if (busy) throw new AppError(409, 'BUSY', 'Another encryption change is in progress');
    busy = true;
    try {
      return await fn();
    } finally {
      busy = false;
    }
  };

  async function openLibrary(codec: CodecHolder): Promise<void> {
    const c = await createContext({
      dataDir,
      watch: opts.watch,
      codec,
      lock,
      launchBackupDelayMs: opts.launchBackupDelayMs,
    });
    const h: { h?: Handler } = {};
    let app: FastifyInstance;
    try {
      app = await track(
        buildApp({
          ctx: c,
          token,
          logger: opts.logger,
          staticDir: opts.staticDir,
          vault: controls,
          notionFetch: opts.notionFetch,
          serverFactory: factory(h),
        }),
        h,
      );
    } catch (e) {
      await c.close();
      throw e;
    }
    ctx = c;
    openApp = app;
    current = handlers.get(app);
    if (codec.encrypting) void normalizeEncryption(c).catch(() => undefined);
  }

  async function closeLibrary(): Promise<void> {
    const c = ctx;
    const app = openApp;
    ctx = undefined;
    openApp = undefined;
    current = lockedApp ? handlers.get(lockedApp) : undefined;
    c?.bus.emit('shutdown');
    await app?.close();
    await c?.close();
  }

  const controls: VaultControls = {
    busy: () => busy,
    status: () => ({ enabled: !!key || !ctx, locked: !ctx, busy }),

    async unlock({ passphrase, recoveryKey, newPassphrase }) {
      if (ctx) throw new AppError(409, 'NOT_LOCKED', 'The library is already unlocked');
      await exclusive(async () => {
        limiter.check();
        let k: Buffer;
        try {
          k = passphrase
            ? await vault.unlockWithPassphrase(passphrase)
            : await vault.unlockWithRecoveryKey(recoveryKey!);
        } catch (e) {
          if (e instanceof AppError && e.code === 'BAD_CREDENTIALS') limiter.fail();
          throw e;
        }
        limiter.ok();
        if (recoveryKey) {
          if (!newPassphrase)
            throw new AppError(
              400,
              'NEW_PASSPHRASE_REQUIRED',
              'Choose a new passphrase to finish recovery',
            );
          await vault.setPassphrase(k, newPassphrase);
        }
        await openLibrary(new CodecHolder(new AesGcmCodec(k)));
        key = k;
        const old = lockedApp;
        lockedApp = undefined;
        await old?.close().catch(() => undefined);
      });
    },

    async lock() {
      if (!key) throw new AppError(409, 'NOT_ENCRYPTED', 'Encryption is not enabled');
      await exclusive(async () => {
        const h: { h?: Handler } = {};
        lockedApp = await track(
          buildLockedApp({
            token,
            logger: opts.logger,
            staticDir: opts.staticDir,
            vault: controls,
            serverFactory: factory(h),
          }),
          h,
        );
        await closeLibrary();
        key = undefined;
      });
    },

    async enable({ passphrase, deleteOldBackups }) {
      const c = requireOpen();
      if (key) throw new AppError(409, 'ALREADY_ENABLED', 'Encryption is already enabled');
      return exclusive(async () => {
        const created = await vault.create(passphrase);
        const aes = new AesGcmCodec(created.key);
        try {
          await recodeLibrary(c, new PlainCodec(), aes);
          if (deleteOldBackups) {
            for (const b of await c.backups.list()) await fs.rm(b.file, { force: true });
            await fs.rm(path.join(dataDir, '.quarantine'), { recursive: true, force: true });
          }
          await c.backups.create('encryption-enabled');
        } catch (e) {
          // Put everything back the way it was rather than leave a half-converted library.
          await recodeLibrary(c, aes, new PlainCodec()).catch(() => undefined);
          await vault.remove();
          throw e;
        }
        key = created.key;
        return { recoveryKey: created.recoveryKey };
      });
    },

    async disable({ passphrase }) {
      const c = requireOpen();
      if (!key) throw new AppError(409, 'NOT_ENCRYPTED', 'Encryption is not enabled');
      await verify(passphrase);
      await exclusive(async () => {
        await recodeLibrary(c, new AesGcmCodec(key!), new PlainCodec());
        await vault.remove();
        key = undefined;
      });
    },

    async changePassphrase({ current: cur, next }) {
      requireOpen();
      if (!key) throw new AppError(409, 'NOT_ENCRYPTED', 'Encryption is not enabled');
      await verify(cur);
      await vault.setPassphrase(key, next);
    },

    async newRecoveryKey({ passphrase }) {
      requireOpen();
      if (!key) throw new AppError(409, 'NOT_ENCRYPTED', 'Encryption is not enabled');
      await verify(passphrase);
      return { recoveryKey: await vault.newRecoveryKey(key) };
    },
  };

  try {
    if (await vault.isEnabled()) {
      const h: { h?: Handler } = {};
      lockedApp = await track(
        buildLockedApp({
          token,
          logger: opts.logger,
          staticDir: opts.staticDir,
          vault: controls,
          serverFactory: factory(h),
        }),
        h,
      );
      current = handlers.get(lockedApp);
    } else {
      await openLibrary(new CodecHolder());
    }
    await new Promise<void>((resolve, reject) => {
      server.once('error', reject);
      server.listen(opts.port ?? 0, '127.0.0.1', resolve);
    });
  } catch (e) {
    await ctx?.close();
    await lock.release();
    throw e;
  }
  const addr = server.address();
  const port = typeof addr === 'object' && addr ? addr.port : 0;
  return {
    url: `http://127.0.0.1:${port}`,
    token,
    get ctx() {
      return requireOpen();
    },
    get locked() {
      return !ctx;
    },
    async close() {
      ctx?.bus.emit('shutdown');
      server.closeAllConnections();
      await new Promise<void>((r) => server.close(() => r()));
      await openApp?.close();
      await lockedApp?.close().catch(() => undefined);
      await ctx?.close();
      await lock.release();
    },
  };
}
