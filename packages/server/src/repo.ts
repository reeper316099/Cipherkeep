import { EventEmitter } from 'node:events';
import { promises as fs } from 'node:fs';
import path from 'node:path';
import { z } from 'zod';
import { ulid } from 'ulid';
import { JsonStore, type JsonFile } from '@cipherkeep/jsonstore';
import { SCHEMAS, type CollectionName, type Common } from '@cipherkeep/shared';
import { AppError, notFound, slugify } from './util.ts';

export type Entity = Common & { [k: string]: unknown };

export interface ListQuery {
  category?: string;
  tag?: string;
  favorite?: boolean;
  archived?: 'true' | 'false' | 'all';
  sort?: string;
  order?: 'asc' | 'desc';
  limit?: number;
  offset?: number;
  /** When set, restricts to (and orders by) these ids. */
  ids?: string[];
}

export interface RepoEvents {
  upsert: [entity: Entity];
  remove: [id: string];
  rebuild: [];
}

export interface Repo extends EventEmitter<RepoEvents> {
  readonly name: CollectionName;
  init(): Promise<void>;
  all(): Entity[];
  get(id: string): Entity | undefined;
  create(input: unknown, opts?: { id?: string }): Promise<Entity>;
  patch(id: string, input: unknown): Promise<Entity>;
  remove(id: string): Promise<void>;
  /** Put an entity back exactly as given (undo); no timestamp changes. */
  restore(entity: Entity): Promise<void>;
  reloadAll(): Promise<void>;
  close?(): Promise<void>;
}

export function queryEntities(all: Entity[], q: ListQuery) {
  let items = q.ids
    ? q.ids.map((id) => all.find((e) => e.id === id)).filter((e): e is Entity => !!e)
    : all.slice();
  const arch = q.archived ?? 'false';
  if (arch !== 'all') items = items.filter((e) => e.archived === (arch === 'true'));
  if (q.category) items = items.filter((e) => e.category === q.category);
  if (q.tag) items = items.filter((e) => e.tags.includes(q.tag!));
  if (q.favorite !== undefined) items = items.filter((e) => e.favorite === q.favorite);
  if (q.sort) {
    const dir = q.order === 'desc' ? -1 : 1;
    const key = q.sort;
    items.sort((a, b) => {
      const x = a[key] as string | number | undefined;
      const y = b[key] as string | number | undefined;
      return x === y ? 0 : x === undefined ? 1 : y === undefined ? -1 : x < y ? -dir : dir;
    });
  } else if (!q.ids) {
    items.sort((a, b) => a.title.localeCompare(b.title));
  }
  const total = items.length;
  const offset = q.offset ?? 0;
  const limit = Math.min(q.limit ?? 50, 500);
  return { items: items.slice(offset, offset + limit), total, limit, offset };
}

type ShapeSchema = z.ZodObject<z.ZodRawShape>;

/** Build create/patch input schemas from a collection's entity schema. */
export function inputSchemas(name: CollectionName) {
  const full = SCHEMAS[name] as unknown as ShapeSchema;
  const create = full
    .omit({ id: true, createdAt: true, updatedAt: true })
    .partial({ slug: true, title: true });
  const patch = full.omit({ id: true, createdAt: true, updatedAt: true }).partial();
  return { full, create, patch };
}

function prepareCreate(name: CollectionName, input: unknown): unknown {
  if (!input || typeof input !== 'object' || Array.isArray(input)) {
    throw new AppError(400, 'VALIDATION', 'Body must be an object');
  }
  const o = { ...(input as Record<string, unknown>) };
  if (o.title == null && typeof o.name === 'string') o.title = o.name;
  if (o.title == null && name === 'log' && typeof o.date === 'string') o.title = o.date;
  return o;
}

function finishCreate(parsed: Record<string, unknown>, id?: string): Entity {
  const now = new Date().toISOString();
  const title = String(parsed.title ?? '');
  if (!title.trim()) throw new AppError(400, 'VALIDATION', 'title is required');
  return {
    ...parsed,
    title,
    slug: parsed.slug ? String(parsed.slug) : slugify(title),
    id: id ?? ulid(),
    createdAt: now,
    updatedAt: now,
  } as Entity;
}

/** Which JSON shard file an entity lives in. */
export function shardOf(name: CollectionName, e: Entity): string {
  switch (name) {
    case 'commands':
    case 'concepts':
      return `data/${name}/${slugify(e.category || 'uncategorized')}.json`;
    case 'labs':
      return `data/labs/${slugify(String(e.platform || 'other'))}.json`;
    case 'log':
      return `data/log/${String(e.date).slice(0, 7)}.json`;
    case 'projects':
      return 'data/projects/projects.json';
    default:
      return `data/${name}/${name}.json`;
  }
}

type Shard = { items: Entity[] };

export class JsonRepo extends EventEmitter<RepoEvents> implements Repo {
  private shards = new Map<string, JsonFile<Shard>>();
  private index = new Map<string, { e: Entity; rel: string }>();
  private schemas;

  constructor(
    private readonly store: JsonStore,
    readonly name: CollectionName,
  ) {
    super();
    this.schemas = inputSchemas(name);
    store.on('reload', (rel) => {
      if (this.shards.has(rel)) {
        this.reindex(rel);
        this.emit('rebuild');
      }
    });
  }

  async init(): Promise<void> {
    const dir = this.store.abs(`data/${this.name}`);
    await fs.mkdir(dir, { recursive: true });
    // Sorted, so the order of entities does not depend on the operating system's directory order.
    for (const f of (await fs.readdir(dir)).sort()) {
      if (f.endsWith('.json')) await this.shard(`data/${this.name}/${f}`);
    }
  }

  private async shard(rel: string): Promise<JsonFile<Shard>> {
    const existing = this.shards.get(rel);
    if (existing) return existing;
    const file = await this.store.open<Shard>(rel, {
      version: 1,
      defaults: () => ({ items: [] }),
      validator: z.object({ items: z.array(this.schemas.full) }) as unknown as {
        parse(x: unknown): Shard;
      },
    });
    this.shards.set(rel, file);
    this.reindex(rel);
    return file;
  }

  private reindex(rel: string): void {
    for (const [id, v] of this.index) if (v.rel === rel) this.index.delete(id);
    for (const e of this.shards.get(rel)!.data.items) this.index.set(e.id, { e, rel });
  }

  all = () => [...this.index.values()].map((v) => v.e);
  get = (id: string) => this.index.get(id)?.e;

  async reloadAll(): Promise<void> {
    for (const f of this.shards.values()) await f.reloadExternal();
    for (const rel of this.shards.keys()) this.reindex(rel);
    this.emit('rebuild');
  }

  async create(input: unknown, opts?: { id?: string }): Promise<Entity> {
    const parsed = this.schemas.create.parse(prepareCreate(this.name, input));
    const entity = finishCreate(parsed, opts?.id);
    const f = await this.shard(shardOf(this.name, entity));
    await f.update((d) => void d.items.push(entity));
    this.reindex(f.rel);
    const saved = this.get(entity.id)!;
    this.emit('upsert', saved);
    return saved;
  }

  async patch(id: string, input: unknown): Promise<Entity> {
    const cur = this.index.get(id);
    if (!cur) throw notFound(this.name, id);
    const patch = this.schemas.patch.parse(input ?? {});
    const merged = { ...cur.e, ...patch, id, createdAt: cur.e.createdAt } as Entity;
    if (patch.title && !patch.slug) merged.slug = slugify(String(patch.title));
    merged.updatedAt = new Date().toISOString();
    await this.replace(cur, merged);
    const saved = this.get(id)!;
    this.emit('upsert', saved);
    return saved;
  }

  private async replace(cur: { e: Entity; rel: string }, merged: Entity): Promise<void> {
    const id = merged.id;
    const newRel = shardOf(this.name, merged);
    const oldFile = this.shards.get(cur.rel)!;
    if (newRel === cur.rel) {
      await oldFile.update((d) => void (d.items = d.items.map((e) => (e.id === id ? merged : e))));
    } else {
      const newFile = await this.shard(newRel);
      await this.store.transaction([oldFile, newFile], ([a, b]) => {
        a!.items = a!.items.filter((e) => e.id !== id);
        b!.items.push(merged);
      });
      this.reindex(cur.rel);
    }
    this.reindex(newRel);
  }

  async restore(entity: Entity): Promise<void> {
    const cur = this.index.get(entity.id);
    if (cur) await this.replace(cur, entity);
    else {
      const f = await this.shard(shardOf(this.name, entity));
      await f.update((d) => void d.items.push(entity));
      this.reindex(f.rel);
    }
    this.emit('upsert', this.get(entity.id)!);
  }

  async remove(id: string): Promise<void> {
    const cur = this.index.get(id);
    if (!cur) throw notFound(this.name, id);
    const f = this.shards.get(cur.rel)!;
    await f.update((d) => void (d.items = d.items.filter((e) => e.id !== id)));
    this.reindex(cur.rel);
    this.emit('remove', id);
  }
}

export { path };
