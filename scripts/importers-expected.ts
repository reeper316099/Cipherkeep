// What the TypeScript implementation returns for the import and export code: text parsers, planners (Notion export, CSV,
// Markdown folder, starter pack), exports of a shared library, and the writeup sanitiser. Used by
// scripts/gen-importer-fixtures.ts to write fixtures/compat/importers/expected.json and by a test that checks the file
// is still current. The Go port (go/internal/importer and friends) is compared against it.
import { cpSync, readFileSync, readdirSync, rmSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import AdmZip from 'adm-zip';
import {
  COLLECTIONS,
  bulletItems,
  cellText,
  classifyRisk,
  decodeNotionPath,
  detectPlaceholders,
  fencedBlocks,
  fillPlaceholders,
  headingKey,
  mapDifficulty,
  normalizeBody,
  parseCommandPage,
  parseConceptPage,
  parseLogEntry,
  parseNotionDate,
  parseNotionPage,
  parseRelationCell,
  parseTables,
  parseTimeSpent,
  repairTables,
  sanitizeWriteup,
  splitExampleLine,
  splitList,
  splitNotionName,
  splitSections,
  unescapeAll,
  type CollectionName,
} from '../packages/shared/src/index.ts';
import { createContext } from '../packages/server/src/context.ts';
import {
  exportCsv,
  exportJson,
  exportMarkdown,
  csvTemplate,
} from '../packages/server/src/export.ts';
import {
  planCsv,
  planJsonBackup,
  planMarkdownFolder,
} from '../packages/server/src/import/generic.ts';
import { planNotion } from '../packages/server/src/import/notion.ts';
import { VirtualFs, fromFolder } from '../packages/server/src/import/source.ts';
import { planStarter } from '../packages/server/src/starter.ts';
import { parseFrontmatter } from '../packages/server/src/notes.ts';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
export const FIXTURE = path.join(root, 'fixtures/synthetic/notion-export');

const vfsOf = (files: Record<string, string>) => {
  const v = new VirtualFs();
  for (const [p, t] of Object.entries(files)) v.add(p, Buffer.from(t, 'utf8'));
  return v;
};

const NBSP = ' ';
const times = [
  '~1.5 hours',
  '1 hr',
  '45 mins',
  '2h 30m',
  '1:30',
  '25:61',
  'half an hour',
  'an hour and 15 minutes',
  'about 2 hrs',
  'approx. 3 hrs',
  'roughly an hour',
  '1.25h',
  '90',
  'a while',
  '',
  '2 hours 15 minutes',
  '1 hr 30 mins',
  '45m',
  '3 MINS',
  '≈ 2 h',
  `1${NBSP}hr`,
  'around half an hour',
  'an hour',
  '0.5 hours',
  '10 minutes and 5 minutes',
];
const dates = [
  'March 24, 2026',
  '2026-03-24',
  '24 Mar 2026',
  '03/24/2026',
  '@March 24, 2026 → March 25, 2026',
  'Sept. 3rd, 2026',
  'Sept 3, 2026',
  'February 31, 2026',
  '2026-02-30',
  '0050-01-01',
  'Foo 3, 2026',
  'Ma 3, 2026',
  '',
  'soon',
  '2026-13-01',
  '12/31/2026',
  '1/2/2026',
  '1st Jan 2026',
  '  March 5 2026  ',
  '2026-03-24T10:00:00',
  '2024-02-29',
  '2023-02-29',
  'December 1, 2025 12:00 PM',
  '5 may 2026',
  '5 Mayday 2026',
];
const relations = [
  'pwd (Dash/%F0%9F%92%BB%20Commands/pwd%2032df3e3018aa804783b1f8a9de1e33c1.md), a, b (x/b%2032df3e3018aa803c957dd5c1468647de.md)',
  'ls, cd',
  '',
  '   ',
  'Docs (https://example.com/x)',
  '(dir/Name%2032df3e3018aa804783b1f8a9de1e33c1.md)',
  'A (a.md), B (b.md)',
  'Weird (title) here (x/y%2032DF3E3018AA804783B1F8A9DE1E33C1.md)',
  'trailing, (a.md)',
  'one (one.md) two (two.md)',
  'x (https://a.b/c d)',
  'T (%E0%A4%A.md)',
];
const names = [
  'ls 32df3e3018aa80d89e90dddec81b898b.md',
  '\u{1F4DA} Cyber Concepts 32cf3e3018aa8051999ed048cba85e91_all.csv',
  'No id here.md',
  'dir/sub/ls 32DF3E3018AA80D89E90DDDEC81B898B.md',
  'plain.csv',
  '  spaced   .md',
  'x_all.csv',
  'weird 32df3e3018aa80d89e90dddec81b898.md',
];
const headings = [
  '\u{1F9E0} Intuition (VERY IMPORTANT)',
  '\u{1F527} Common Options (Flags)',
  '  --x--  ',
  '123 abc',
  '❓ Questions I still have',
  '!!!',
  'Tools / Commands Related',
];
const paths = [
  'Dash/%F0%9F%92%BB%20Commands/pwd.md',
  'a\\b%zz',
  '100%',
  'caf%C3%A9.md',
  'café.md',
  '%E0%A4%A',
];
const pages: [string, boolean][] = [
  [
    '# ls\n\nCategory: File System\nSyntax: ls \\[flags\\]\n\n## \u{1F4CC} What it does\n\nlists\n',
    true,
  ],
  ['# Page\n\nNote: not a property\n\ntext', false],
  ['﻿# T\r\n\r\nA: 1\r\nnot a prop\r\n\r\nbody', true],
  ['# T\n\n' + 'k'.repeat(70) + ': v\n\nbody', true],
  ['# T\n\nKey:\nOther: x y\n\n', true],
  ['no heading\n\nKey: v\n\nbody', true],
  ['\n\n#   Spaced title  \n\nCategory: A\nCategory: B\n\nbody', true],
  ['# T\n\nKey:value\n\nbody', true],
  ['# T\n\n- dash: no\n\nbody', true],
];
const bodies = [
  'use \\[flags\\] here\n\n```\necho \\[x\\]\n```',
  'a `\\[x\\]` and \\[y\\]',
  '<aside>\n\u{1F4A1} Remember this\n</aside>\n\n<details><summary>More</summary>\nhidden text\n</details>',
  '<aside>\n⚠️ careful\n\nmore\n</aside>',
  '<aside>plain</aside>',
  '| a | b\nc |\n| d | e |',
  '```\n| not\ntable\n```',
  'x  \t\n\n\n\n\ny   ',
  '~~~\n```\n~~~\n\\[ok\\]',
  '| h | i |\n| --- | --- |\n| a\n  b | c |',
];
const sectionBodies = [
  'intro\n\n## A\n\none\n\n---\n\n## \u{1F9E0} B\n\n```\n## not heading\n```',
  '## Only\n\ntext\n\n---\n\n',
  '---\n\nlead\n\n## A\n\n---\n\nx\n\n---',
  '## A\n### B\n##C\n## D  \ntext',
];
const tableBodies = [
  '| a | b |\n| --- | --- |\n| `x\\|y` | z |',
  '| only |\n| :--: |\n| 1 |\n\ntext\n\n| a | b |\n|--|--|\n| 1 | 2 |',
  '```\n| a |\n```\n| real | row |',
  '|a|b|\n|-|-|\n|x|y|',
];
const cells = [
  '`ls` **bold** *it* _u_ a<br/>b \\[x\\]',
  '2*3*4 snake_case_name',
  '**a** and *b*',
  '_x_y_ and _z_',
  'a<BR >b',
  '  \\*esc\\*  ',
];
const fences = [
  'x\n```bash\nls -l\n```\ntext\n~~~\nraw\n~~~',
  '```\nunclosed',
  '```js\na\n```\n```\nb\n```',
  '```\n````\n```',
  'a\n~~~\n```\n~~~',
];
const exampleLines = [
  'ls -al ~ = "lists all"',
  'echo a = "b" = "c"',
  'plain command',
  '',
  '# comment',
  ' = "no command"',
  'x = "trailing"  ',
];
const placeholderTexts = [
  'ls [flags] <target> {ip} [[:upper:]]* [!abc]',
  '<a-b> {c_d} [e f] <1x>',
  'no tokens',
];
const risks: [string, string[]][] = [
  ['rm', []],
  ['mv', []],
  ['ls', []],
  ['find', ['find . -exec rm -rf {} +']],
  ['  RM  -rf', []],
  ['x', ['dd if=a of=/dev/sda']],
  ['', []],
  ['y', [':(){ :|:& };:']],
  ['z', ['mkfs.ext4 /dev/sdb']],
];
const bulletBodies = [
  '- Step 1\n- real\n1. numbered\n-\n',
  '* a\n+ b\n  - nested\n10. ten\n',
  'plain\n- ok',
];

const commandSample = {
  name: 'show',
  category: 'General',
  props: { Syntax: 'show \\[flags\\] \\[target\\]', Example: 'show -a', Explanation: 'shows' },
  body: [
    '## \u{1F4CC} What it does',
    '',
    'Shows things',
    '',
    '---',
    '',
    '## \u{1F9E9} Arguments',
    '',
    '(Inputs the command takes.)',
    '',
    '- target → thing to show',
    '- `-x`: flag',
    '- bare',
    '',
    '---',
    '',
    '## \u{1F527} Common Options (Flags)',
    '',
    '| Option | Meaning |',
    '| --- | --- |',
    '| -a | all |',
    '| -l | long |',
    '',
    '---',
    '',
    '## \u{1F4A1} Examples',
    '',
    '```bash',
    'show -a x = "all of x"',
    '```',
    '',
    '| `show -l y` | Long y |',
    '| --- | --- |',
    '| `show -a z` | All z |',
    '',
    '## \u{1F9EA} Extra',
    '',
    'kept',
  ].join('\n'),
};
const commandSample2 = {
  name: 'less',
  props: { Category: 'Files' } as Record<string, string>,
  body: '## \u{1F4CC} What it does\n\nPager\n\n## Commands\n\n| Key | Action |\n| --- | --- |\n| q | quit |\n| /x | search |\n\n## \u{1F3AF} Basic Syntax\n\n```bash\nless <file>\n```\n',
};
const conceptSample = {
  name: 'X',
  difficulty: 'Beginner',
  tags: ['a', 'b'],
  body: '## \u{1F9E0} What is it?\n\ntext\n\n---\n\n## \u{1F6E0}️ Tools / Commands Related\n\n- cp\n- find\n\n---\n\n## ❓ Questions I still have\n\n- one?\n- \n- two?\n\n## \u{1F381} Custom\n\nmine\n\n## Difficulty Level\n\nadvanced\n\n## Related Concepts\n\n- Y\n- Component 1',
};
const logSamples = [
  {
    name: 'March 24, 2026',
    date: 'March 24, 2026',
    timeSpent: '~1.5 hours',
    topics: 'A, B',
    keyTakeaway: 'tk',
    body: '## \u{1F4DA} What I learned\n\n- less\n- df',
  },
  { name: 'Notes', timeSpent: 'a while', body: '' },
  { name: 'Mar 3 2026', date: '', body: '' },
  { name: 'x', date: 'nonsense', topics: ',,', body: '## What I learned\n\n-\n- one' },
];
const sanitizeCases: [string, string[]][] = [
  [
    'flag: picoCTF{s3cr3t_v4lue} at 192.168.1.20 and mail a.b@ex.org; token=abc123 Bearer abcdefgh12345678',
    [],
  ],
  [
    '-----BEGIN RSA PRIVATE KEY-----\nabc\n-----END RSA PRIVATE KEY-----\nhost lab-7.internal',
    ['lab-\\d+\\.internal', '(x+)+y', '['],
  ],
  ['Used nmap 7.94 on port 443 (version 1.2.3)', []],
  ['AKIA' + 'ABCDEFGHIJKLMNOP' + ' and 999.1.1.1 and 10.0.0.255 password: hunter2', []],
  ['Host LAB-9.Internal', ['lab-\\d+\\.internal']],
  ['api_key = abc and API-KEY:xyz and secret= q', []],
  ['x@y.z, e@f.g.h and flag{ab} and FLAG{abcd}', []],
];

function parserBattery() {
  const trim = (m: Record<string, string>) => m;
  return {
    times: times.map((t) => parseTimeSpent(t)),
    dates: dates.map((d) => parseNotionDate(d)),
    relations: relations.map((r) =>
      parseRelationCell(r).map((x) => ({ title: x.title, path: x.path ?? null, id: x.id ?? null })),
    ),
    names: names.map((n) => {
      const s = splitNotionName(n);
      return { title: s.title, id: s.id ?? null, ext: s.ext, all: s.all };
    }),
    headings: headings.map(headingKey),
    paths: paths.map(decodeNotionPath),
    lists: ['a, b; c ,,', '', ' ; ', 'one'].map((l) => splitList(l)),
    pages: pages.map(([t, e]) => {
      const p = parseNotionPage(t, e);
      return { title: p.title, props: trim(p.props), body: p.body };
    }),
    bodies: bodies.map(normalizeBody),
    repaired: bodies.map(repairTables),
    unescaped: ['ls \\[flags\\] \\*', 'a\\\\b \\_x\\_ \\`t\\`', 'no escapes'].map(unescapeAll),
    sections: sectionBodies.map((b) => {
      const s = splitSections(b);
      return {
        preamble: s.preamble,
        sections: s.sections.map((x) => ({ title: x.title, key: x.key, markdown: x.markdown })),
      };
    }),
    tables: tableBodies.map((b) => parseTables(b).map((t) => t.rows)),
    cells: cells.map(cellText),
    fences: fences.map((f) => fencedBlocks(f)),
    exampleLines: exampleLines.map((l) => splitExampleLine(l)),
    placeholders: placeholderTexts.map((t) => detectPlaceholders(t)),
    filled: placeholderTexts.map((t) =>
      fillPlaceholders(t, { flags: '-a', target: 'host', ip: '' }),
    ),
    risks: risks.map(([n, e]) => classifyRisk(n, e)),
    bullets: bulletBodies.map(bulletItems),
    difficulties: ['Beginner', 'advanced', 'Wizard', '', ' Hard ', 'MEDIUM'].map((d) =>
      mapDifficulty(d),
    ),
    commands: [commandSample, commandSample2].map((c) => parseCommandPage(c as never)),
    concept: parseConceptPage(conceptSample),
    logs: logSamples.map((l) => parseLogEntry(l as never)),
    sanitize: sanitizeCases.map(([t, p]) => sanitizeWriteup(t, p)),
  };
}

const csvSamples: { collection: CollectionName; text: string }[] = [
  {
    collection: 'commands',
    text: [
      'Name,Category,Syntax,Risk,Tags,Options,Placeholders,id,createdAt,Extra Column',
      `"'=cmd",General,'=cmd,safe,a|b,"[{""flag"":""-x"",""meaning"":""x""}]",,SHOULD_BE_DROPPED,2020-01-01,keep me`,
      'ls,File System,"ls [flags]",caution,"x; y",not json,"[""flags""]",,,',
      ',General,nameless,,,,,,,',
      '',
      'cd,General,cd <dir>,SAFE,,,,,,',
    ].join('\r\n'),
  },
  {
    collection: 'labs',
    text: [
      'Title,Platform,Status,Points,Hints Used,Writeup Private,Tags',
      'Lab A,Bandit,Completed,10,2,yes,"one, two"',
      'Lab B,,Not Started,abc,,no,',
    ].join('\n'),
  },
  { collection: 'notes', text: 'title,folder,markdown,kind\nN,"a/b","# h\n\nbody",guide\n' },
];

const mdFiles: Record<string, string> = {
  'top.md': '# Top Note\n\nbody text\n',
  'sub/with-front.md':
    '---\ntitle: Front Title\ntags: [x, y]\ncategory: Cat\ncustom: 1\nother:\n  nested: true\n---\n\n# Ignored H1\n\ncontent\n',
  'sub/tags-string.md': '---\ntags: "a, b"\n---\nx',
  'sub/deep/no-title.md': 'just text',
  'cmd.md':
    '---\nck_collection: commands\ntitle: mycmd\ncategory: General\nsyntax: mycmd [x]\nexplanation: does it\nexample: mycmd -x\nrisk: caution\ntags: [t1]\n---\n\n## \u{1F4CC} What it does\n\nStuff\n\n## \u{1F527} Common Options (Flags)\n\n| Option | Meaning |\n| --- | --- |\n| -x | ex |\n',
  'con.md':
    '---\nck_collection: concepts\ntitle: MyConcept\ndifficulty: Intermediate\ntags: [c]\n---\n\n## One\n\nfirst\n\n---\n\n## Two\n\nsecond',
  'readme.txt': 'ignored',
};

const jsonBackup = JSON.stringify({
  app: 'Cipherkeep',
  format: 1,
  collections: {
    notes: [
      {
        id: '01HZZZZZZZZZZZZZZZZZZZZZZ1',
        title: 'Backed up',
        createdAt: '2025-01-01T00:00:00.000Z',
        updatedAt: '2025-01-02T00:00:00.000Z',
        folder: 'f',
        markdown: 'm',
        slug: 'backed-up',
      },
    ],
    commands: [
      {
        id: '01HZZZZZZZZZZZZZZZZZZZZZZ2',
        name: 'bk',
        title: 'bk',
        category: 'General',
        createdAt: '2025-01-01T00:00:00.000Z',
        updatedAt: '2025-01-02T00:00:00.000Z',
        slug: 'bk',
      },
      { title: 'invalid: no id' },
    ],
  },
});

export async function computeExpected(libDir: string) {
  const notionVfs = await fromFolder(FIXTURE);
  const scratch = path.join(os.tmpdir(), `ck-importers-${process.pid}-${Date.now()}`);
  cpSync(libDir, scratch, { recursive: true });
  const ctx = await createContext({ dataDir: scratch, watch: false });
  try {
    const mdZip = new AdmZip(await exportMarkdown(ctx));
    const vault = mdZip
      .getEntries()
      .map((e) => {
        const text = e.getData().toString('utf8');
        if (!e.entryName.endsWith('.md') || !text.startsWith('---\n'))
          return { name: e.entryName, text };
        const { data, body } = parseFrontmatter(text);
        return { name: e.entryName, frontmatter: data, body };
      })
      .sort((a, b) => (a.name < b.name ? -1 : 1));
    const priv = new AdmZip(await exportMarkdown(ctx, { includePrivate: true }));
    const jsonDoc = exportJson(ctx) as unknown as Record<string, unknown>;
    return {
      parsers: parserBattery(),
      plans: {
        notion: planNotion(notionVfs, { sourceName: 'notion-export' }),
        starter: planStarter(),
        csv: csvSamples.map((s) => {
          const v = vfsOf({ 'in.csv': s.text });
          return planCsv(v, { sourceName: 'in.csv', collection: s.collection });
        }),
        markdown: planMarkdownFolder(vfsOf(mdFiles), { sourceName: 'folder' }),
        jsonBackup: planJsonBackup(vfsOf({ 'backup.json': jsonBackup }), {
          sourceName: 'backup.json',
        }),
      },
      exports: {
        csv: Object.fromEntries(COLLECTIONS.map((c) => [c, exportCsv(ctx, c)])),
        csvPrivate: exportCsv(ctx, 'labs', { includePrivate: true }),
        templates: Object.fromEntries(COLLECTIONS.map((c) => [c, csvTemplate(c)])),
        vault,
        vaultPrivateLab: priv
          .getEntries()
          .filter((e) => e.entryName.startsWith('labs/'))
          .sort((a, b) => (a.entryName < b.entryName ? -1 : 1))
          .map((e) => e.getData().toString('utf8').includes('flag is here')),
        json: {
          app: jsonDoc.app,
          format: jsonDoc.format,
          includesPrivateWriteups: jsonDoc.includesPrivateWriteups,
          collections: jsonDoc.collections,
        },
      },
    };
  } finally {
    rmSync(scratch, { recursive: true, force: true });
  }
}

export function sourceFixtureFiles(): string[] {
  return readdirSync(FIXTURE);
}
export const readText = (p: string) => readFileSync(p, 'utf8');
