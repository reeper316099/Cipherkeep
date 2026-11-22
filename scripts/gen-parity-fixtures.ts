// Builds the shared library the Go port is compared against (fixtures/compat/parity) and records what the TypeScript
// implementation returns for it (expected.json). Re-run only when behaviour is meant to change:
//   npx tsx scripts/gen-parity-fixtures.ts
// Everything that depends on the clock uses the fixed time NOW below.
import { mkdirSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { createContext } from '../packages/server/src/context.ts';
import { commitPlan } from '../packages/server/src/import/index.ts';
import { planStarter } from '../packages/server/src/starter.ts';

const root = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  '..',
  'fixtures',
  'compat',
  'parity',
);
const lib = path.join(root, 'library');
import { NOW } from './parity-expected.ts';
const DAY = 86_400_000;
const at = (days: number, hours = 0) => new Date(NOW.getTime() - days * DAY - hours * 3_600_000);

rmSync(root, { recursive: true, force: true });
mkdirSync(lib, { recursive: true });

const ctx = await createContext({ dataDir: lib, watch: false });
await commitPlan(ctx, planStarter(), { strategy: 'update-empty-only' });
const r = ctx.repos;

// ---- extra content: labs, projects, logs, snippets, notes with wiki links, an archived item ----
const concepts = r.concepts.all();
const commands = r.commands.all();
const cmdByName = (n: string) => commands.find((c) => c.name === n)!;
const conceptTitles = concepts.map((c) => c.title);
await r.labs.create({
  title: 'Bandit 0',
  platform: 'Bandit',
  status: 'Completed',
  difficulty: 'Easy',
  domain: 'linux',
  conceptIds: [concepts[0]!.id],
  commandIds: [cmdByName('ls').id],
  timeSpentMin: 40,
  solvedAt: at(10).toISOString(),
});
await r.labs.create({
  title: 'Bandit 1',
  platform: 'Bandit',
  status: 'In Progress',
  difficulty: 'Medium',
  commandIds: [cmdByName('cat').id],
  timeSpentMin: 25,
});
await r.labs.create({
  title: 'Web 101',
  platform: 'TryHackMe',
  status: 'Completed',
  difficulty: 'Hard',
  domain: 'web security',
  conceptIds: [concepts[2]!.id],
  timeSpentMin: 90,
  solvedAt: at(3).toISOString(),
});
await r.labs.create({
  title: 'Old lab',
  platform: 'picoCTF',
  status: 'Not Started',
  difficulty: 'Easy',
  archived: true,
});
await r.projects.create({
  title: 'Port scanner',
  type: 'Tool',
  status: 'In progress',
  skillsUsed: ['Networking'],
  conceptIds: [concepts[1]!.id],
});
await r.projects.create({ title: 'Site', type: 'Website', status: 'Done' });
for (let i = 0; i < 12; i++)
  await r.log.create({
    title: `Day ${i}`,
    date: at(i * 3)
      .toISOString()
      .slice(0, 10),
    timeSpentMin: 20 + (i % 4) * 15,
    topics: ['linux', 'bash'],
    keyTakeaway: `Learned thing ${i}`,
    commandIds: [cmdByName('ls').id],
  });
await r.snippets.create({
  title: 'Find big files',
  language: 'bash',
  code: 'find / -size +100M 2>/dev/null',
  description: 'Large files',
});
await r.notes.create({
  title: 'Parity hub',
  folder: 'Hub',
  markdown: `Links: [[${conceptTitles[0]}]], [[cmd:ls]], [[Nope]] and \`[[ignored]]\`.\n\n\`\`\`bash\nls -la --color=auto\n\`\`\`\n`,
});
await r.notes.create({
  title: 'Ünïcode Zebra',
  folder: 'Hub/Deep',
  markdown: 'zebra ünïcode **bold** [[Parity hub]]',
});
const archived = await r.commands.create({
  name: 'archivedcmd',
  category: 'General',
  explanation: 'should be hidden',
  archived: true,
});
void archived;
await r.concepts.create({
  title: 'Open questions demo',
  category: 'Linux',
  sections: [
    {
      key: 'questions-i-still-have',
      title: 'Questions',
      markdown: '- Why?\n- [x] Done?\n- How?\n',
    },
  ],
  relatedCommandIds: [cmdByName('grep').id],
});

// ---- a deterministic study history ----
const studyCmds = r.commands
  .all()
  .filter((c) => !c.archived)
  .slice(0, 30);
const ratings = ['good', 'easy', 'hard', 'again', 'good', 'good', 'easy'] as const;
for (const [i, c] of studyCmds.entries()) {
  for (let round = 0; round < 1 + (i % 3); round++) {
    await ctx.study.answer({
      id: c.id,
      rating: ratings[(i + round) % ratings.length]!,
      collection: 'commands',
      now: at(40 - i - round * 6, i % 5),
    });
  }
}
for (const [i, c] of r.concepts.all().slice(0, 6).entries())
  await ctx.study.answer({
    id: c.id,
    rating: i % 2 ? 'easy' : 'good',
    collection: 'concepts',
    now: at(20 - i * 2),
  });
await ctx.close();

// The study calls above recorded activity with the real clock; replace it with a fixed, deterministic history.
rmSync(path.join(lib, 'activity'), { recursive: true, force: true });
mkdirSync(path.join(lib, 'activity'), { recursive: true });
const events: string[] = [];
for (const d of [0, 1, 2, 3, 4, 7, 8, 9, 15, 16, 30, 31, 45])
  for (const [k, type] of ['update', 'study', 'create'].entries())
    events.push(
      JSON.stringify({
        ts: at(d, k).toISOString(),
        type,
        collection: 'commands',
        id: studyCmds[(d + k) % studyCmds.length]!.id,
      }),
    );
events.push(JSON.stringify({ ts: at(1).toISOString(), type: 'import' }));
const byMonth = new Map<string, string[]>();
for (const line of events.sort()) {
  const m = (JSON.parse(line).ts as string).slice(0, 7);
  byMonth.set(m, [...(byMonth.get(m) ?? []), line]);
}
for (const [m, lines] of byMonth)
  writeFileSync(path.join(lib, 'activity', `${m}.jsonl`), lines.join('\n') + '\n');

// Strip anything that is not part of the library proper.
for (const d of ['imports', 'backups', '.lock', '.quarantine', 'study/skill-history.json'])
  rmSync(path.join(lib, d), { recursive: true, force: true });

const { computeExpected } = await import('./parity-expected.ts');
const expected = await computeExpected(lib);
writeFileSync(path.join(root, 'expected.json'), JSON.stringify(expected, null, 1) + '\n');
console.log(
  readdirSync(lib),
  'queue due',
  expected.queue.due,
  'items',
  expected.queue.items.length,
);
