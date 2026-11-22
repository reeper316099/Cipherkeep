// Import/export benchmark (Node side), in process (no HTTP). Pairs with go/cmd/bench-import so both are measured on the
// same on-disk data:   npx tsx scripts/bench-import.ts <dataset-dir> <library-dir>
// <dataset-dir> holds a generated Notion export (export/ and export.zip; made if missing). <library-dir> is an existing
// library (the phase 2 benchmark library) that is copied before use and only read for the export timings.
import AdmZip from 'adm-zip';
import { createHash } from 'node:crypto';
import {
  cpSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { createContext } from '../packages/server/src/context.ts';
import { exportCsv, exportJson, exportMarkdown } from '../packages/server/src/export.ts';
import { commitPlan } from '../packages/server/src/import/commit.ts';
import { planNotion } from '../packages/server/src/import/notion.ts';
import { fromZipBuffer } from '../packages/server/src/import/source.ts';

const dataset = path.resolve(process.argv[2] ?? 'bench-import-data');
const libSrc = path.resolve(process.argv[3] ?? 'bench-lib-data');
const COMMANDS = 2_000;
const CONCEPTS = 100;
const LOOSE = 1_500;
const ROUNDS = 3;

const id32 = (s: string) => createHash('md5').update(s).digest('hex');
let seed = 4242;
const rnd = (n: number) => ((seed = (seed * 1103515245 + 12345) & 0x7fffffff), seed % n);
const WORDS =
  'kernel socket packet handshake payload buffer privilege escalation enumerate banner fingerprint hash salt cipher token session cookie header query shell pipe signal process daemon mount inode symlink checksum archive exploit patch advisory'.split(
    ' ',
  );
const words = (n: number) => Array.from({ length: n }, () => WORDS[rnd(WORDS.length)]).join(' ');
const csvQ = (s: string) => (/[",\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s);

function generate() {
  const root = path.join(dataset, 'export');
  const cmdDir = path.join(root, 'Dash', 'Commands');
  const conDir = path.join(root, 'Dash', 'Concepts');
  mkdirSync(cmdDir, { recursive: true });
  mkdirSync(conDir, { recursive: true });
  const cats = [
    'File System',
    'Networking',
    'Permissions',
    'Text Processing',
    'General',
    'Linux',
    'Bash',
    'Web Security',
  ];
  const rows = ['Commands,Category,Syntax,Example,Explanation,Used In'];
  for (let i = 0; i < COMMANDS; i++) {
    const name = `cmd${i}`;
    const cat = cats[i % cats.length]!;
    rows.push(
      [name, cat, `${name} \\[flags\\] \\[target\\]`, `${name} -a host${i}`, `${words(4)}`, '']
        .map(csvQ)
        .join(','),
    );
    const body = [
      `# ${name}`,
      '',
      `Category: ${cat}`,
      `Syntax: ${name} \\[flags\\] \\[target\\]`,
      `Example: ${name} -a host${i}`,
      `Explanation: ${words(4)}`,
      '',
      '## \u{1F4CC} What it does',
      '',
      words(30),
      '',
      '---',
      '',
      '## \u{1F9E9} Arguments',
      '',
      '(Inputs the command takes.)',
      '',
      `- target → ${words(3)}`,
      '',
      '---',
      '',
      '## \u{1F527} Common Options (Flags)',
      '',
      '| Option | Meaning |',
      '| --- | --- |',
      `| -a | ${words(3)} |`,
      `| -l | ${words(3)} |`,
      `| -v | ${words(3)} |`,
      '',
      '---',
      '',
      '## \u{1F4A1} Examples',
      '',
      '```bash',
      `${name} -a x = "${words(3)}"`,
      `${name} -l y = "${words(3)}"`,
      '```',
      '',
    ].join('\n');
    writeFileSync(path.join(cmdDir, `${name} ${id32('c' + i)}.md`), body);
  }
  writeFileSync(
    path.join(root, 'Dash', `Commands ${id32('cmds')}_all.csv`),
    '﻿' + rows.join('\n') + '\n',
  );
  const crow = ['Name,Category,Difficulty,Tags,Related Commands'];
  for (let i = 0; i < CONCEPTS; i++) {
    const name = `Concept ${i}`;
    const rel = `cmd${i} (Dash/Commands/cmd${i}%20${id32('c' + i)}.md), cmd${i + 1} (Dash/Commands/cmd${i + 1}%20${id32('c' + (i + 1))}.md)`;
    crow.push([name, 'Linux', 'Beginner', 'a, b', rel].map(csvQ).join(','));
    const secs = [
      'What is it?',
      'Intuition',
      'Tools / Commands Related',
      'Questions I still have',
    ].map(
      (t, k) =>
        `## ${t}\n\n${k === 2 ? `- cmd${i}\n- cmd${i + 2}` : k === 3 ? '- one?\n- two?' : words(60)}\n`,
    );
    writeFileSync(
      path.join(conDir, `${name} ${id32('k' + i)}.md`),
      `# ${name}\n\nCategory: Linux\nDifficulty: Beginner\n\n${secs.join('\n---\n\n')}`,
    );
  }
  writeFileSync(
    path.join(root, 'Dash', `Concepts ${id32('cons')}_all.csv`),
    '﻿' + crow.join('\n') + '\n',
  );
  mkdirSync(path.join(root, 'Notes', 'files'), { recursive: true });
  for (let i = 0; i < LOOSE; i++) {
    const dir = path.join(root, 'Notes', `Area ${i % 30}`, `Topic ${i % 7}`);
    mkdirSync(dir, { recursive: true });
    const link =
      `[Other](Notes/Area ${(i + 1) % 30}/Topic ${(i + 1) % 7}/Page ${i + 1} ${id32('p' + (i + 1))}.md)`.replace(
        / /g,
        '%20',
      );
    const att = i % 30 === 0 ? `\n\n![pic](Notes/files/pic${i}.bin)` : '';
    const code = i % 5 === 0 ? `\n\n\`\`\`bash\nnmap -sV host${i}\n\`\`\`\n` : '';
    writeFileSync(
      path.join(dir, `Page ${i} ${id32('p' + i)}.md`),
      `# Page ${i}\n\n${words(150)}\n\n${link}${att}${code}\n`,
    );
    if (i % 30 === 0)
      writeFileSync(path.join(root, 'Notes', 'files', `pic${i}.bin`), Buffer.alloc(20_000, i));
  }
  const zip = new AdmZip();
  zip.addLocalFolder(root);
  zip.writeZip(path.join(dataset, 'export.zip'));
}

if (!existsSync(path.join(dataset, 'export.zip'))) {
  mkdirSync(dataset, { recursive: true });
  generate();
  console.log('generated dataset in', dataset);
}

const ms = (t0: bigint) => Number(process.hrtime.bigint() - t0) / 1e6;
const results: Record<string, number[]> = {};
const rec = (k: string, v: number) => (results[k] ??= []).push(v);
const zipBuf = readFileSync(path.join(dataset, 'export.zip'));

const libCopy = mkdtempSync(path.join(os.tmpdir(), 'ck-bench-imp-lib-'));
cpSync(libSrc, libCopy, { recursive: true });
rmSync(path.join(libCopy, '.lock'), { force: true });
const lib = await createContext({ dataDir: libCopy, watch: false });

for (let r = 0; r < ROUNDS; r++) {
  let t = process.hrtime.bigint();
  const vfs = fromZipBuffer(zipBuf);
  rec('read zip into memory', ms(t));
  t = process.hrtime.bigint();
  const plan = planNotion(vfs, { sourceName: 'bench' });
  rec('plan Notion export', ms(t));
  const dir = mkdtempSync(path.join(os.tmpdir(), 'ck-bench-imp-'));
  const ctx = await createContext({ dataDir: dir, watch: false });
  t = process.hrtime.bigint();
  const rep = await commitPlan(ctx, plan, { strategy: 'update-empty-only' }, { vfs });
  rec('commit (create items, write files)', ms(t));
  if (r === 0)
    console.log(
      'items',
      plan.items.length,
      'created',
      JSON.stringify(rep.created),
      'attachments',
      rep.attachments,
    );
  // a second commit of the same plan changes nothing: that is the re-import ("update") cost
  t = process.hrtime.bigint();
  await commitPlan(
    ctx,
    planNotion(vfs, { sourceName: 'bench', lookup: undefined }),
    { strategy: 'update-empty-only' },
    { vfs },
  );
  rec('re-import of the same export (no changes)', ms(t));
  await ctx.close();
  rmSync(dir, { recursive: true, force: true });

  t = process.hrtime.bigint();
  const vault = await exportMarkdown(lib);
  rec('export Markdown vault (15k+ files)', ms(t));
  if (r === 0) console.log('vault bytes', vault.length);
  t = process.hrtime.bigint();
  const csv = exportCsv(lib, 'commands');
  rec('export commands as CSV', ms(t));
  t = process.hrtime.bigint();
  const json = JSON.stringify(exportJson(lib));
  rec('export everything as JSON', ms(t));
  if (r === 0) console.log('csv bytes', csv.length, 'json bytes', json.length);
}
await lib.close();
rmSync(libCopy, { recursive: true, force: true });

const med = (xs: number[]) => [...xs].sort((a, b) => a - b)[Math.floor(xs.length / 2)]!;
for (const [k, v] of Object.entries(results))
  console.log(
    `${k.padEnd(48)} ${med(v).toFixed(1).padStart(9)} ms   (${Math.min(...v).toFixed(1)} to ${Math.max(...v).toFixed(1)})`,
  );
console.log(`peak RSS ${(process.memoryUsage().rss / 1048576).toFixed(0)} MB`);
