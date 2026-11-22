import { createHash } from 'node:crypto';
import path from 'node:path';
import Papa from 'papaparse';
import {
  decodeNotionPath,
  fencedBlocks,
  mapOutsideFences,
  normalizeTitle,
  parseCommandPage,
  parseConceptPage,
  parseLogEntry,
  parseNotionPage,
  parseRelationCell,
  splitList,
  splitNotionName,
  type CollectionName,
} from '@cipherkeep/shared';
import { slugify } from '../util.ts';
import type { Plan, PlanItem, PlanWarning, RelTarget, PlanDatabase } from './types.ts';
import type { VirtualFs } from './source.ts';

export interface Lookup {
  /** Existing entity id in the store by collection + normalised title. */
  find(collection: CollectionName, normTitle: string): string | undefined;
}

export interface NotionPlanOptions {
  sourceName: string;
  lookup?: Lookup;
  /** csv path → collection (or 'skip') overrides for unknown/misdetected databases. */
  mapping?: Record<string, CollectionName | 'skip'>;
}

type Row = Record<string, string>;

const DB_SIGNATURES: [CollectionName, string[]][] = [
  ['commands', ['Syntax', 'Explanation']],
  ['labs', ['Platform', 'Commands Used']],
  ['concepts', ['Related Commands']],
  ['log', ['Time Spent', 'Key Takeaway']],
  ['projects', ['Type', 'Skills Used']],
];

export function detectCollection(headers: string[]): CollectionName | null {
  const set = new Set(headers.map((h) => h.trim()));
  for (const [c, req] of DB_SIGNATURES) if (req.every((h) => set.has(h))) return c;
  return null;
}

const hash8 = (s: string) => createHash('sha1').update(s).digest('hex').slice(0, 8);
const LAB_STATUS: Record<string, string> = {
  'not started': 'Not Started',
  todo: 'Not Started',
  'in progress': 'In Progress',
  started: 'In Progress',
  completed: 'Completed',
  done: 'Completed',
  complete: 'Completed',
};
const PROJECT_STATUS: Record<string, string> = {
  'not started': 'Not started',
  todo: 'Not started',
  'in progress': 'In progress',
  done: 'Done',
  completed: 'Done',
};

interface PendingRel {
  field: string; // entity field (or `links:<type>`)
  target: CollectionName;
  refs?: { title: string; id?: string }[];
  titles?: string[]; // free-text mentions: resolved silently unless `warnUnresolved`
  warnUnresolved?: boolean;
}

interface Built {
  item: PlanItem;
  pending: PendingRel[];
}

export function planNotion(vfs: VirtualFs, opts: NotionPlanOptions): Plan {
  const warnings: PlanWarning[] = [];
  const warn = (level: PlanWarning['level'], code: string, message: string, source?: string) =>
    warnings.push({ level, code, message, source });
  const paths = vfs.paths();
  const mds = paths.filter((p) => p.toLowerCase().endsWith('.md'));
  const csvs = paths.filter((p) => p.toLowerCase().endsWith('.csv'));
  const otherFiles = paths.filter((p) => !/\.(md|csv)$/i.test(p));
  const used = new Set<string>(); // md paths consumed by a database / template / hub

  // ---- databases (dedupe `_all.csv`) ----
  const byKey = new Map<string, string>();
  for (const p of csvs) {
    const n = splitNotionName(p);
    const key = `${path.posix.dirname(p)}/${n.title}|${n.id ?? ''}`;
    if (!byKey.has(key) || (!n.all && splitNotionName(byKey.get(key)!).all)) byKey.set(key, p);
  }
  const dbDirs: {
    csv: string;
    folder: string;
    title: string;
    collection: CollectionName | null;
    rows: Row[];
    headers: string[];
  }[] = [];
  const databases: PlanDatabase[] = [];
  for (const csv of [...byKey.values()].sort()) {
    const parsed = Papa.parse<Row>(vfs.text(csv), { header: true, skipEmptyLines: 'greedy' });
    const headers = (parsed.meta.fields ?? []).map((h) => h.trim());
    const title = splitNotionName(csv).title;
    const override = opts.mapping?.[csv];
    const collection = override === 'skip' ? null : (override ?? detectCollection(headers));
    const folder = `${path.posix.dirname(csv) === '.' ? '' : path.posix.dirname(csv) + '/'}${title}`;
    const rows = parsed.data.map((r) =>
      Object.fromEntries(Object.entries(r).map(([k, v]) => [k.trim(), (v ?? '').trim()])),
    );
    databases.push({ csv, title, collection, rows: rows.length, headers });
    if (!collection) {
      if (override !== 'skip')
        warn(
          'warning',
          'UNKNOWN_DATABASE',
          `Database "${title}" was not recognised (headers: ${headers.join(', ')}); its pages are imported as notes`,
          csv,
        );
      continue;
    }
    dbDirs.push({ csv, folder, title, collection, rows, headers });
  }

  const built: Built[] = [];
  const templates: Plan['templates'] = [];
  const pageIdToRef = new Map<string, { ref: string; collection: CollectionName }>();
  const byTitle = new Map<CollectionName, Map<string, string>>();
  const registerTitle = (c: CollectionName, title: string, ref: string) => {
    const m = byTitle.get(c) ?? new Map<string, string>();
    if (!m.has(normalizeTitle(title))) m.set(normalizeTitle(title), ref);
    byTitle.set(c, m);
  };

  // ---- rows + pages per database ----
  for (const db of dbDirs) {
    const collection = db.collection!;
    const titleCol = db.headers[0]!;
    let pages = mds.filter(
      (p) => p.startsWith(db.folder + '/') && p.slice(db.folder.length + 1).indexOf('/') < 0,
    );
    if (!pages.length) {
      // flat layout: pages next to the CSV
      const dir = path.posix.dirname(db.csv);
      const titles = new Set(db.rows.map((r) => normalizeTitle(r[titleCol] ?? '')));
      pages = mds.filter(
        (p) =>
          path.posix.dirname(p) === dir && titles.has(normalizeTitle(splitNotionName(p).title)),
      );
    }
    const pageMeta = pages.map((p) => ({ path: p, ...splitNotionName(p), taken: false }));
    const mkItem = (title: string, row: Row | null, page?: (typeof pageMeta)[number]) => {
      const text = page ? vfs.text(page.path) : '';
      if (page) used.add(page.path);
      const parsed = page ? parseNotionPage(text, true) : { title, props: {}, body: '' };
      const props: Row = { ...parsed.props };
      for (const [k, v] of Object.entries(row ?? {})) if (v) props[k] = v;
      const ref = page?.id ?? `csv:${collection}:${normalizeTitle(title)}`;
      const sourceName = page?.path ?? db.csv;
      const warns: string[] = [];
      const pending: PendingRel[] = [];
      let input: Record<string, unknown>;
      switch (collection) {
        case 'commands': {
          const r = parseCommandPage({
            name: title,
            category: props.Category,
            props,
            body: parsed.body,
          });
          input = r.input;
          warns.push(...r.warnings);
          if (props['Used In'])
            pending.push({
              field: 'links:used-in',
              target: 'labs',
              refs: parseRelationCell(props['Used In']),
            });
          break;
        }
        case 'concepts': {
          const r = parseConceptPage({
            name: title,
            category: props.Category,
            difficulty: props.Difficulty,
            tags: splitList(props.Tags),
            body: parsed.body,
          });
          input = r.input;
          warns.push(...r.warnings);
          pending.push({
            field: 'relatedCommandIds',
            target: 'commands',
            refs: parseRelationCell(props['Related Commands']),
            titles: r.commandMentions,
            warnUnresolved: true,
          });
          pending.push({
            field: 'relatedConceptIds',
            target: 'concepts',
            titles: r.conceptMentions,
          });
          break;
        }
        case 'labs': {
          const status = LAB_STATUS[(props.Status ?? '').toLowerCase()] ?? 'Not Started';
          input = {
            title,
            platform: props.Platform ?? '',
            category: props.Platform ?? '',
            status,
            difficulty: props.Difficulty || 'Easy',
            writeup: parsed.body,
          };
          pending.push({
            field: 'commandIds',
            target: 'commands',
            refs: parseRelationCell(props['Commands Used']),
            warnUnresolved: true,
          });
          pending.push({
            field: 'conceptIds',
            target: 'concepts',
            refs: parseRelationCell(props['Skills Used']),
            warnUnresolved: true,
          });
          break;
        }
        case 'projects': {
          const status = PROJECT_STATUS[(props.Status ?? '').toLowerCase()];
          if (props.Status && !status) warns.push(`unknown status "${props.Status}"`);
          const skills = splitList(props['Skills Used']);
          input = {
            title,
            type: props.Type || 'Script',
            category: props.Type ?? '',
            status: status ?? 'Not started',
            skillsUsed: skills,
            notes: parsed.body,
            ...(skills.length ? { tags: skills.map((s) => s.toLowerCase()) } : {}),
          };
          pending.push({ field: 'conceptIds', target: 'concepts', titles: skills });
          break;
        }
        default: {
          const r = parseLogEntry({
            name: title,
            date: props.Date,
            timeSpent: props['Time Spent'],
            topics: props['Topics Covered'],
            keyTakeaway: props['Key Takeaway'],
            body: parsed.body,
          });
          input = r.input;
          warns.push(...r.warnings);
          pending.push({ field: 'commandIds', target: 'commands', titles: r.learned });
          pending.push({ field: 'conceptIds', target: 'concepts', titles: r.learned });
        }
      }
      // keep any property we did not map, so nothing is lost
      const known = new Set([
        'Category',
        'Example',
        'Explanation',
        'Syntax',
        'Used In',
        'Difficulty',
        'Tags',
        'Related Commands',
        'Platform',
        'Status',
        'Commands Used',
        'Skills Used',
        'Type',
        'Date',
        'Time Spent',
        'Topics Covered',
        'Key Takeaway',
        titleCol,
      ]);
      const unknown = Object.fromEntries(Object.entries(props).filter(([k]) => !known.has(k)));
      if (Object.keys(unknown).length) {
        input.extras = { ...(input.extras as object | undefined), notionProperties: unknown };
        warns.push(
          `kept ${Object.keys(unknown).length} unmapped propert${Object.keys(unknown).length === 1 ? 'y' : 'ies'} in extras`,
        );
      }
      const item: PlanItem = {
        collection,
        ref,
        title,
        input,
        relations: [],
        source: sourceName,
        warnings: warns,
      };
      built.push({ item, pending });
      if (page?.id) pageIdToRef.set(page.id, { ref, collection });
      registerTitle(collection, title, ref);
    };

    for (const row of db.rows) {
      const title = row[titleCol] ?? '';
      if (!title) continue;
      const page = pageMeta.find(
        (p) => !p.taken && normalizeTitle(p.title) === normalizeTitle(title),
      );
      if (page) page.taken = true;
      else
        warn(
          'info',
          'ROW_WITHOUT_PAGE',
          `Row "${title}" has no page body; imported from CSV only`,
          db.csv,
        );
      mkItem(title, row, page);
    }
    for (const page of pageMeta.filter((p) => !p.taken)) {
      if (/template/i.test(page.title)) {
        used.add(page.path);
        const { sections } = parseSections(vfs.text(page.path));
        templates.push({
          collection,
          source: page.path,
          template: {
            name: page.title.replace(/\s*template\s*/i, '').trim() || page.title,
            sections,
          },
        });
      } else {
        warn(
          'warning',
          'ORPHAN_PAGE',
          `Page "${page.title}" is in ${db.title} but has no CSV row; imported from page properties`,
          page.path,
        );
        mkItem(page.title, null, page);
      }
    }
  }

  // ---- hub pages (link to database CSVs): skipped by default ----
  const csvNames = new Set(csvs.map((c) => path.posix.basename(c)));
  const attachments = new Map<string, { from: string; to: string; bytes: number }>();
  const attachmentUse = new Set<string>();

  // ---- loose pages ----
  const looseBuilt: Built[] = [];
  for (const p of mds) {
    if (used.has(p)) continue;
    const name = splitNotionName(p);
    const raw = vfs.text(p);
    const isHub = [...raw.matchAll(/\]\(([^)]+\.csv)\)/g)].some((m) =>
      csvNames.has(path.posix.basename(decodeNotionPath(m[1]!))),
    );
    const dirSegs = path.posix.dirname(p) === '.' ? [] : path.posix.dirname(p).split('/');
    const parsed = parseNotionPage(raw, false);
    const title = parsed.title || name.title;
    // A template page whose database has no CSV (e.g. an empty Labs database): infer from the folder name.
    const folderName = splitNotionName(dirSegs[dirSegs.length - 1] ?? '').title;
    const tplCollection =
      /\btemplate\b/i.test(title) && !Object.keys(parsed.props).length
        ? collectionFromFolder(folderName)
        : null;
    if (tplCollection) {
      templates.push({
        collection: tplCollection,
        source: p,
        template: {
          name: title.replace(/\s*template\s*/i, '').trim() || title,
          sections: parseSections(raw).sections,
        },
      });
      continue;
    }
    const { body } = rewriteLinks(
      parsed.body,
      path.posix.dirname(p),
      vfs,
      attachments,
      attachmentUse,
    );
    const folder = dirSegs.map((s) => slugify(splitNotionName(s).title)).join('/'); // same form NoteRepo stores
    const blocks = fencedBlocks(body);
    const codeChars = blocks.reduce((n, b) => n + b.code.length, 0);
    const ratio = body.length ? codeChars / body.length : 0;
    const noteInput = { title, folder, markdown: body, kind: guessKind(title), category: '' };
    const lang = [...blocks].sort((a, b) => b.code.length - a.code.length)[0]?.lang || 'text';
    const snippetInput = {
      title,
      language: lang,
      code: blocks.map((b) => b.code).join('\n\n'),
      description: body
        .replace(/```[\s\S]*?```/g, '')
        .replace(/\s+/g, ' ')
        .trim()
        .slice(0, 300)
        .replace(/[\ud800-\udbff]$/, ''), // never leave half of an emoji behind
    };
    const asSnippet = blocks.length > 0 && ratio > 0.6;
    const item: PlanItem = {
      collection: asSnippet ? 'snippets' : 'notes',
      ref: name.id ?? `md:${normalizeTitle(folder + '/' + title)}`,
      title,
      input: asSnippet ? snippetInput : noteInput,
      relations: [],
      source: p,
      warnings: [],
      alternatives: { notes: noteInput, snippets: snippetInput },
      ...(isHub
        ? {
            skipByDefault:
              'dashboard hub page (progress and navigation are computed natively); override to import as a note',
          }
        : {}),
    };
    if (Object.keys(parsed.props).length) item.input.extras = { notionProperties: parsed.props };
    looseBuilt.push({ item, pending: [] });
    if (name.id) pageIdToRef.set(name.id, { ref: item.ref, collection: item.collection });
  }
  built.push(...looseBuilt);

  // ---- second pass: relations ----
  const resolve = (
    target: CollectionName,
    ref: { title: string; id?: string },
  ): RelTarget | undefined => {
    if (ref.id) {
      const hit = pageIdToRef.get(ref.id);
      if (hit && hit.collection === target) return { ref: hit.ref };
    }
    const k = normalizeTitle(ref.title);
    const inPlan = byTitle.get(target)?.get(k);
    if (inPlan) return { ref: inPlan };
    const existing = opts.lookup?.find(target, k);
    return existing ? { id: existing } : undefined;
  };
  for (const { item, pending } of built) {
    for (const p of pending) {
      const targets: RelTarget[] = [];
      const seen = new Set<string>();
      const add = (t: RelTarget | undefined) => {
        if (!t) return false;
        const key = 'ref' in t ? 'r:' + t.ref : 'i:' + t.id;
        if (seen.has(key) || ('ref' in t && t.ref === item.ref)) return true;
        seen.add(key);
        targets.push(t);
        return true;
      };
      for (const r of p.refs ?? []) {
        if (!add(resolve(p.target, r)))
          item.warnings.push(`unresolved ${p.target} relation: "${r.title}"`);
      }
      for (const t of p.titles ?? []) {
        if (
          !add(resolve(p.target, { title: t })) &&
          p.warnUnresolved &&
          /^[\w.+-]{1,40}$/.test(t)
        ) {
          item.warnings.push(`mentioned ${p.target.replace(/s$/, '')} "${t}" not found`);
        }
      }
      if (targets.length) item.relations.push({ field: p.field, targets });
    }
  }

  const items = built.map((b) => b.item);
  const referenced = [...attachments.values()];
  const unreferenced = otherFiles.filter((f) => !attachmentUse.has(f));
  if (unreferenced.length)
    warn(
      'info',
      'UNREFERENCED_FILES',
      `${unreferenced.length} file(s) in the export are not referenced by any page and were not imported`,
    );
  return {
    kind: 'notion',
    sourceName: opts.sourceName,
    items,
    templates,
    attachments: referenced,
    databases,
    warnings,
  };
}

function parseSections(text: string) {
  const page = parseNotionPage(text, false);
  const lines = page.body.split('\n');
  const sections: { key: string; title: string; hint?: string }[] = [];
  let cur: { title: string; lines: string[] } | null = null;
  const flush = () => {
    if (!cur) return;
    const hint = cur.lines
      .join('\n')
      .replace(/^\s*(?:---+\s*\n?)+|(?:\n?\s*---+\s*)+$/g, '')
      .trim();
    sections.push({
      key: cur.title
        .replace(/^[^\p{L}\p{N}]+/u, '')
        .toLowerCase()
        .replace(/[^a-z0-9]+/g, '-')
        .replace(/^-|-$/g, ''),
      title: cur.title,
      ...(hint ? { hint } : {}),
    });
  };
  for (const l of lines) {
    const h = /^##\s+(.*)$/.exec(l);
    if (h) {
      flush();
      cur = { title: h[1]!.trim(), lines: [] };
    } else cur?.lines.push(l);
  }
  flush();
  return { sections };
}

function collectionFromFolder(name: string): CollectionName | null {
  if (/command/i.test(name)) return 'commands';
  if (/concept/i.test(name)) return 'concepts';
  if (/\blabs?\b/i.test(name)) return 'labs';
  if (/project/i.test(name)) return 'projects';
  if (/log/i.test(name)) return 'log';
  return null;
}

function guessKind(title: string): 'cheatsheet' | 'guide' | 'writeup-notes' | 'general' {
  if (/cheat\s*-?sheet/i.test(title)) return 'cheatsheet';
  if (/write-?up/i.test(title)) return 'writeup-notes';
  if (/guide|setup|configuration|how to/i.test(title)) return 'guide';
  return 'general';
}

/** Page links → [[wiki links]]; local files → ck:// attachment URLs (recorded for copying). */
function rewriteLinks(
  body: string,
  pageDir: string,
  vfs: VirtualFs,
  attachments: Map<string, { from: string; to: string; bytes: number }>,
  used: Set<string>,
): { body: string; rewrites: number } {
  let rewrites = 0;
  const out = mapOutsideFences(body, (line) =>
    line.replace(
      /(!?)\[([^\]]*)\]\(([^)\s]+)\)/g,
      (m, bang: string, text: string, href: string) => {
        if (/^[a-z][a-z0-9+.-]*:/i.test(href) || href.startsWith('#')) return m;
        const decoded = decodeNotionPath(href);
        const resolved = path.posix.normalize(
          path.posix.join(pageDir === '.' ? '' : pageDir, decoded),
        );
        if (/\.md$/i.test(resolved)) {
          rewrites++;
          return `[[${splitNotionName(resolved).title}${text && text !== splitNotionName(resolved).title ? '|' + text : ''}]]`;
        }
        if (/\.csv$/i.test(resolved)) return text;
        const file = vfs.files.get(resolved);
        if (!file) return m;
        used.add(resolved);
        const to = `${hash8(resolved)}/${path.posix.basename(resolved)}`;
        attachments.set(resolved, { from: resolved, to, bytes: file.length });
        rewrites++;
        return `${bang}[${text}](ck://attachments/${to.split('/').map(encodeURIComponent).join('/')})`;
      },
    ),
  );
  return { body: out, rewrites };
}
