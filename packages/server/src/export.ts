import AdmZip from 'adm-zip';
import { promises as fs } from 'node:fs';
import path from 'node:path';
import Papa from 'papaparse';
import YAML from 'yaml';
import { z } from 'zod';
import { COLLECTIONS, SCHEMAS, APP_NAME, type CollectionName } from '@cipherkeep/shared';
import { readDecoded } from '@cipherkeep/jsonstore';
import type { Context } from './context.ts';
import type { Entity } from './repo.ts';

export interface ExportOptions {
  /** Include lab writeups flagged `writeupPrivate` (excluded by default). */
  includePrivate?: boolean;
}

const str = (v: unknown) => (typeof v === 'string' ? v : '');
const arr = <T>(v: unknown) => (Array.isArray(v) ? (v as T[]) : []);
const withheld = (e: Entity, o: ExportOptions) => e.writeupPrivate === true && !o.includePrivate;
const PRIVATE_NOTE = '_Private writeup omitted from this export._';

const safeName = (s: string) =>
  s
    .replace(/[\\/:*?"<>|#^[\]]/g, '-')
    .replace(/\s+/g, ' ')
    .trim() || 'untitled';

function fm(data: Record<string, unknown>): string {
  const clean = Object.fromEntries(
    Object.entries(data).filter(
      ([, v]) => v !== undefined && v !== '' && !(Array.isArray(v) && !v.length),
    ),
  );
  return `---\n${YAML.stringify(clean).trimEnd()}\n---\n\n`;
}

const wiki = (ctx: Context, c: CollectionName, ids: unknown) =>
  arr<string>(ids)
    .map((id) => ctx.repos[c].get(id))
    .filter((e): e is Entity => !!e)
    .map((e) => `[[${str(e.title)}]]`);

const cell = (s: string) => s.replace(/\|/g, '\\|').replace(/\n/g, ' ');

function commandMd(e: Entity): string {
  const opts = arr<{ flag: string; meaning: string }>(e.options);
  const args = arr<{ name: string; meaning: string }>(e.arguments);
  const exs = arr<{ command: string; description: string }>(e.examples);
  const parts = [
    fm({
      ck_collection: 'commands',
      ck_id: e.id,
      title: e.title,
      category: e.category,
      tags: e.tags,
      risk: e.risk,
      platforms: e.platforms,
      syntax: e.syntax,
      explanation: e.explanation,
    }),
    '## 📌 What it does',
    '',
    str(e.whatItDoes),
    '',
    '---',
    '',
    '## ⚙️ Basic Syntax',
    '',
    str(e.syntax),
    '',
  ];
  if (args.length)
    parts.push(
      '---',
      '',
      '## 🧩 Arguments',
      '',
      ...args.map((a) => `- ${a.name} → ${a.meaning}`),
      '',
    );
  if (opts.length)
    parts.push(
      '---',
      '',
      '## 🔧 Common Options (Flags)',
      '',
      '| Option | Meaning |',
      '| --- | --- |',
      ...opts.map((o) => `| ${cell(o.flag)} | ${cell(o.meaning)} |`),
      '',
    );
  if (exs.length)
    parts.push(
      '---',
      '',
      '## 💡 Examples',
      '',
      '```bash',
      ...exs.map((x) => (x.description ? `${x.command} = "${x.description}"` : x.command)),
      '```',
      '',
    );
  if (str(e.notes)) parts.push('', str(e.notes), '');
  return parts.join('\n');
}

function conceptMd(ctx: Context, e: Entity): string {
  const body = arr<{ title: string; markdown: string }>(e.sections)
    .map((s) => `## ${s.title}\n\n${s.markdown}`)
    .join('\n\n---\n\n');
  return (
    fm({
      ck_collection: 'concepts',
      ck_id: e.id,
      title: e.title,
      category: e.category,
      difficulty: e.difficulty,
      tags: e.tags,
      related_commands: wiki(ctx, 'commands', e.relatedCommandIds),
      related_concepts: wiki(ctx, 'concepts', e.relatedConceptIds),
    }) +
    body +
    '\n'
  );
}

function entityFile(
  ctx: Context,
  c: CollectionName,
  e: Entity,
  o: ExportOptions,
): { rel: string; text: string } {
  const name = safeName(str(e.title));
  switch (c) {
    case 'commands':
      return { rel: `commands/${safeName(str(e.name || e.title))}.md`, text: commandMd(e) };
    case 'concepts':
      return { rel: `concepts/${name}.md`, text: conceptMd(ctx, e) };
    case 'labs':
      return {
        rel: `labs/${safeName(str(e.platform) || 'Other')}/${name}.md`,
        text:
          fm({
            ck_collection: 'labs',
            ck_id: e.id,
            title: e.title,
            platform: e.platform,
            status: e.status,
            difficulty: e.difficulty,
            domain: e.domain,
            points: e.points,
            solvedAt: e.solvedAt,
            timeSpentMin: e.timeSpentMin,
            hintsUsed: e.hintsUsed,
            tags: e.tags,
            commands: wiki(ctx, 'commands', e.commandIds),
            concepts: wiki(ctx, 'concepts', e.conceptIds),
          }) +
          (withheld(e, o) ? PRIVATE_NOTE : str(e.writeup)) +
          '\n',
      };
    case 'projects':
      return {
        rel: `projects/${name}.md`,
        text:
          fm({
            ck_collection: 'projects',
            ck_id: e.id,
            title: e.title,
            type: e.type,
            status: e.status,
            skills: e.skillsUsed,
            repo: e.repoUrl,
            tags: e.tags,
            concepts: wiki(ctx, 'concepts', e.conceptIds),
          }) +
          str(e.notes) +
          '\n',
      };
    case 'log':
      return {
        rel: `log/${str(e.date) || name}.md`,
        text:
          fm({
            ck_collection: 'log',
            ck_id: e.id,
            title: e.title,
            date: e.date,
            timeSpentMin: e.timeSpentMin,
            timeSpent: e.timeSpentRaw,
            topics: e.topics,
            takeaway: e.keyTakeaway,
            commands: wiki(ctx, 'commands', e.commandIds),
            concepts: wiki(ctx, 'concepts', e.conceptIds),
          }) +
          str(e.body) +
          '\n',
      };
    case 'notes':
      return {
        rel: `notes/${str(e.folder) ? str(e.folder) + '/' : ''}${str(e.slug) || name}.md`,
        text:
          fm({
            ck_collection: 'notes',
            ck_id: e.id,
            title: e.title,
            category: e.category,
            tags: e.tags,
            kind: e.kind,
          }) + str(e.markdown),
      };
    default:
      return {
        rel: `snippets/${name}.md`,
        text:
          fm({
            ck_collection: 'snippets',
            ck_id: e.id,
            title: e.title,
            language: e.language,
            file: e.filePath,
            tags: e.tags,
          }) +
          (str(e.description) ? str(e.description) + '\n\n' : '') +
          (e.code ? '```' + str(e.language) + '\n' + str(e.code) + '\n```\n' : ''),
      };
  }
}

/** ck://attachments/x/y → path relative to the markdown file (works in Obsidian). */
function relinkAttachments(text: string, fileRel: string): string {
  const depth = fileRel.split('/').length - 1;
  const up = depth ? '../'.repeat(depth) : './';
  return text.replace(/ck:\/\/attachments\//g, `${up}attachments/`);
}

export async function exportMarkdown(ctx: Context, o: ExportOptions = {}): Promise<Buffer> {
  const zip = new AdmZip();
  const used = new Set<string>();
  for (const c of COLLECTIONS) {
    for (const e of ctx.repos[c].all()) {
      const f = entityFile(ctx, c, e, o);
      let rel = f.rel;
      for (let n = 2; used.has(rel.toLowerCase()); n++) rel = f.rel.replace(/\.md$/, ` (${n}).md`);
      used.add(rel.toLowerCase());
      zip.addFile(rel, Buffer.from(relinkAttachments(f.text, rel), 'utf8'));
    }
  }
  const addDir = async (dir: string, prefix: string) => {
    let ents;
    try {
      ents = await fs.readdir(dir, { withFileTypes: true });
    } catch {
      return;
    }
    for (const ent of ents) {
      if (ent.isDirectory()) await addDir(path.join(dir, ent.name), `${prefix}/${ent.name}`);
      else if (ent.isFile())
        zip.addFile(
          `${prefix}/${ent.name}`,
          await readDecoded(ctx.store.codec, path.join(dir, ent.name)),
        );
    }
  };
  await addDir(ctx.store.abs('attachments'), 'attachments');
  zip.addFile(
    'README.md',
    Buffer.from(
      `# ${APP_NAME} export\n\nObsidian-compatible vault. Files carry \`ck_collection\`/\`ck_id\` frontmatter so ${APP_NAME} can re-import them.\n`,
    ),
  );
  return zip.toBuffer();
}

type Kind = 'string' | 'number' | 'boolean' | 'strings' | 'json';
function unwrap(t: z.ZodTypeAny): z.ZodTypeAny {
  let cur = t;
  for (let i = 0; i < 6; i++) {
    const def = cur._def as { innerType?: z.ZodTypeAny; typeName: string };
    if (def.innerType && /Optional|Default|Nullable/.test(def.typeName)) cur = def.innerType;
    else break;
  }
  return cur;
}
function kindOf(t: z.ZodTypeAny): Kind {
  const u = unwrap(t);
  const n = (u._def as { typeName: string }).typeName;
  if (n === 'ZodNumber') return 'number';
  if (n === 'ZodBoolean') return 'boolean';
  if (n === 'ZodArray')
    return (unwrap((u as z.ZodArray<z.ZodTypeAny>).element)._def as { typeName: string })
      .typeName === 'ZodString'
      ? 'strings'
      : 'json';
  if (n === 'ZodObject' || n === 'ZodRecord') return 'json';
  return 'string';
}

/** Spreadsheet formula-injection guard; the CSV importer strips the leading `'`. */
export const guardCell = (s: string) => (/^[=+\-@\t\r]/.test(s) ? `'${s}` : s);

export function csvColumns(c: CollectionName): string[] {
  return Object.keys((SCHEMAS[c] as unknown as z.ZodObject<z.ZodRawShape>).shape);
}

export function exportCsv(ctx: Context, c: CollectionName, o: ExportOptions = {}): string {
  const shape = (SCHEMAS[c] as unknown as z.ZodObject<z.ZodRawShape>).shape;
  const cols = Object.keys(shape);
  const rows = ctx.repos[c].all().map((e) =>
    cols.map((k) => {
      let v = e[k];
      if (c === 'labs' && k === 'writeup' && withheld(e, o)) v = '';
      if (v === undefined || v === null) return '';
      const kind = kindOf(shape[k]!);
      return guardCell(
        kind === 'strings'
          ? (v as string[]).join('; ')
          : kind === 'json'
            ? JSON.stringify(v)
            : String(v),
      );
    }),
  );
  return Papa.unparse({ fields: cols, data: rows }, { newline: '\n' }) + '\n';
}

export function csvTemplate(c: CollectionName): string {
  return (
    Papa.unparse(
      {
        fields: csvColumns(c).filter(
          (k) => !['id', 'createdAt', 'updatedAt', 'extras'].includes(k),
        ),
        data: [],
      },
      { newline: '\n' },
    ).trimEnd() + '\n'
  );
}

export function exportJson(ctx: Context, o: ExportOptions = {}) {
  const collections = Object.fromEntries(
    COLLECTIONS.map((c) => [
      c,
      ctx.repos[c].all().map((e) => (c === 'labs' && withheld(e, o) ? { ...e, writeup: '' } : e)),
    ]),
  );
  return {
    app: APP_NAME,
    format: 1,
    exportedAt: new Date().toISOString(),
    includesPrivateWriteups: !!o.includePrivate,
    collections,
    config: {
      settings: ctx.settings.data,
      categories: ctx.categories.data,
      skills: ctx.skills.data,
    },
  };
}
