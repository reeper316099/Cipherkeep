// Library benchmark (Node side), in-process (no HTTP). Pairs with go/cmd/bench-lib so both are measured on the same
// on-disk library:   npx tsx scripts/bench-lib.ts <dataset-dir>      (generated if missing)
import { execFileSync } from 'node:child_process';
import { cpSync, existsSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { ulid } from 'ulid';
import { createContext } from '../packages/server/src/context.ts';
import { buildOverview } from '../packages/server/src/overview.ts';
import { queryEntities } from '../packages/server/src/repo.ts';

const dataset = path.resolve(process.argv[2] ?? 'bench-lib-data');
const NOTES = 10_000;
const COMMANDS = 5_000;
const WORDS =
  'kernel socket packet handshake payload buffer privilege escalation enumerate banner fingerprint hash salt cipher token session cookie header query shell pipe signal process daemon mount inode symlink checksum archive payload exploit patch advisory'.split(
    ' ',
  );
let seed = 12345;
const rnd = (n: number) => ((seed = (seed * 1103515245 + 12345) & 0x7fffffff), seed % n);
const words = (n: number) => Array.from({ length: n }, () => WORDS[rnd(WORDS.length)]).join(' ');
const stamp = '2026-06-01T00:00:00.000Z';

function generate() {
  for (let i = 0; i < NOTES; i++) {
    const folder = `area-${i % 40}/topic-${i % 7}`;
    mkdirSync(path.join(dataset, 'data/notes', folder), { recursive: true });
    const body = `# Note ${i} ${words(3)}\n\n${words(150)}\n\n\`\`\`bash\nnmap -sV -p ${rnd(65000)} host${i}\n\`\`\`\n\n${words(120)}\n`;
    writeFileSync(
      path.join(dataset, 'data/notes', folder, `note-${i}.md`),
      `---\nid: ${ulid()}\ntitle: Note ${i} ${WORDS[i % WORDS.length]}\nkind: general\ntags:\n  - ${WORDS[i % 9]}\ncreatedAt: ${stamp}\nupdatedAt: ${stamp}\n---\n${body}`,
    );
  }
  const common = (t: string) => ({
    id: ulid(),
    title: t,
    slug: t.toLowerCase().replace(/\W+/g, '-'),
    createdAt: stamp,
    updatedAt: stamp,
  });
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
  const shard = (rel: string, items: unknown[]) => {
    mkdirSync(path.dirname(path.join(dataset, rel)), { recursive: true });
    writeFileSync(path.join(dataset, rel), JSON.stringify({ schemaVersion: 1, data: { items } }));
  };
  const cmdIds: string[] = [];
  for (const [ci, cat] of cats.entries()) {
    const items = [];
    for (let i = ci; i < COMMANDS; i += cats.length) {
      const c = {
        ...common(`cmd${i}`),
        name: `cmd${i}`,
        category: cat,
        syntax: `cmd${i} [flags] <target>`,
        explanation: words(8),
        whatItDoes: words(25),
        mastery: rnd(4),
        options: [
          { flag: '-a', meaning: words(4) },
          { flag: `--opt${i}=x`, meaning: words(4) },
        ],
        examples: [{ command: `cmd${i} -a ${WORDS[i % WORDS.length]}`, description: words(5) }],
      };
      cmdIds.push(c.id);
      items.push(c);
    }
    shard(`data/commands/${cat.toLowerCase().replace(/\W+/g, '-')}.json`, items);
  }
  const conceptIds: string[] = [];
  for (const cat of ['Linux', 'Networking', 'Web Security', 'Bash']) {
    const items = Array.from({ length: 50 }, (_, i) => ({
      ...common(`Concept ${cat} ${i}`),
      category: cat,
      difficulty: ['Easy', 'Medium', 'Hard'][i % 3],
      confidence: rnd(4),
      sections: [
        { key: 'what-is-it', title: 'What is it?', markdown: words(60) },
        {
          key: 'questions-i-still-have',
          title: 'Q',
          markdown: `- ${words(5)}?\n- [x] ${words(4)}?\n`,
        },
      ],
      relatedCommandIds: [cmdIds[rnd(cmdIds.length)], cmdIds[rnd(cmdIds.length)]],
    }));
    conceptIds.push(...items.map((c) => c.id));
    shard(`data/concepts/${cat.toLowerCase().replace(/\W+/g, '-')}.json`, items);
  }
  shard(
    'data/labs/bandit.json',
    Array.from({ length: 100 }, (_, i) => ({
      ...common(`Lab ${i}`),
      platform: 'Bandit',
      status: ['Not Started', 'In Progress', 'Completed'][i % 3],
      difficulty: ['Easy', 'Medium', 'Hard'][i % 3],
      conceptIds: [conceptIds[rnd(conceptIds.length)]],
      commandIds: [cmdIds[rnd(cmdIds.length)]],
      timeSpentMin: 10 + rnd(60),
    })),
  );
  shard(
    'data/log/2026-05.json',
    Array.from({ length: 150 }, (_, i) => ({
      ...common(`Day ${i}`),
      date: `2026-05-${String(1 + (i % 28)).padStart(2, '0')}`,
      timeSpentMin: 20 + rnd(60),
      topics: ['linux'],
      keyTakeaway: words(6),
      body: words(30),
    })),
  );
  mkdirSync(path.join(dataset, 'activity'), { recursive: true });
  writeFileSync(
    path.join(dataset, 'activity/2026-05.jsonl'),
    Array.from({ length: 300 }, (_, i) =>
      JSON.stringify({
        ts: `2026-05-${String(1 + (i % 28)).padStart(2, '0')}T10:00:00.000Z`,
        type: 'update',
        collection: 'commands',
        id: cmdIds[i % cmdIds.length],
      }),
    ).join('\n') + '\n',
  );
}

const rssMb = () =>
  Number(execFileSync('ps', ['-o', 'rss=', '-p', String(process.pid)], { encoding: 'utf8' })) /
  1024;
const median = (xs: number[]) => [...xs].sort((a, b) => a - b)[Math.floor(xs.length / 2)]!;
const time = async (n: number, fn: () => unknown | Promise<unknown>) => {
  const ts: number[] = [];
  for (let i = 0; i < n; i++) {
    const t = performance.now();
    await fn();
    ts.push(performance.now() - t);
  }
  return median(ts);
};
const omitMd = (es: Record<string, unknown>[]) =>
  JSON.stringify(es.map(({ markdown: _m, ...rest }) => rest));

async function run() {
  const tmp = mkdtempSync(path.join(os.tmpdir(), 'ck-bl-'));
  cpSync(dataset, tmp, { recursive: true });
  const res: Record<string, number> = {};
  const t = performance.now();
  const ctx = await createContext({ dataDir: tmp, watch: false });
  res['open library (load 10k notes + 5k commands, index) ms'] = performance.now() - t;
  const notes = ctx.repos.notes.all();
  res['search "nmap" ms'] = await time(15, () => ctx.search.search('nmap'));
  res['search "-sV" ms'] = await time(15, () => ctx.search.search('-sV'));
  res['search cmd:cmd42 ms'] = await time(15, () => ctx.search.search('cmd:cmd42'));
  res['search prefix "kern" notes ms'] = await time(15, () =>
    ctx.search.search('kern', { types: ['notes'] }),
  );
  res['search typo "enumarate" ms'] = await time(15, () => ctx.search.search('enumarate'));
  res['list 500 notes sorted, bodies omitted (JSON) ms'] = await time(10, () =>
    omitMd(queryEntities(ctx.repos.notes.all(), { limit: 500 }).items as never),
  );
  res['graph (JSON) ms'] = await time(10, () => JSON.stringify(ctx.links.graph({})));
  res['dashboard overview ms'] = await time(5, async () =>
    JSON.stringify(await buildOverview(ctx, { now: new Date('2026-06-01T12:00:00Z') })),
  );
  res['get one note ms'] = await time(2000, () => ctx.repos.notes.get(notes[5000]!.id));
  let i = 0;
  res['patch one note (write to disk) ms'] = await time(20, () =>
    ctx.repos.notes.patch(notes[100 + i++]!.id, { markdown: 'edited ' + i }),
  );
  res['patch one command (write shard) ms'] = await time(20, () =>
    ctx.repos.commands.patch(ctx.repos.commands.all()[10 + i++]!.id, { notes: 'x' + i }),
  );
  res['memory, RSS MB'] = rssMb();
  await ctx.close();
  rmSync(tmp, { recursive: true, force: true });
  return res;
}

if (!existsSync(path.join(dataset, 'data/notes'))) generate();
const runs: Record<string, number>[] = [];
for (let i = 0; i < 3; i++) runs.push(await run());
const keys = Object.keys(runs[0]!);
console.log(
  JSON.stringify({
    engine: 'node',
    results: Object.fromEntries(keys.map((k) => [k, +median(runs.map((r) => r[k]!)).toFixed(2)])),
    min: Object.fromEntries(keys.map((k) => [k, +Math.min(...runs.map((r) => r[k]!)).toFixed(2)])),
    max: Object.fromEntries(keys.map((k) => [k, +Math.max(...runs.map((r) => r[k]!)).toFixed(2)])),
  }),
);
