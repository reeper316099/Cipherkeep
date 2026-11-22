// What the TypeScript implementation returns for the shared parity library (fixtures/compat/parity/library). Used by
// scripts/gen-parity-fixtures.ts to write expected.json and by a test that checks expected.json is still current.
import { cpSync, rmSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { COLLECTIONS, schedule, type CollectionName } from '../packages/shared/src/index.ts';
import { createContext } from '../packages/server/src/context.ts';
import { buildOverview } from '../packages/server/src/overview.ts';
import { queryEntities } from '../packages/server/src/repo.ts';

export const NOW = new Date('2026-11-30T12:00:00.000Z');

export async function computeExpected(lib: string) {
  const scratch = path.join(os.tmpdir(), `ck-parity-${process.pid}-${Date.now()}`);
  cpSync(lib, scratch, { recursive: true }); // the dashboard writes a skill-history snapshot
  const c2 = await createContext({ dataDir: scratch, watch: false });
  try {
    const cmdByName = (n: string) => c2.repos.commands.all().find((c) => c.name === n)!;
    const concepts = c2.repos.concepts.all();
    const round = (v: unknown): unknown => (typeof v === 'number' ? Math.round(v * 1e6) / 1e6 : v);
    const queries = [
      'ls',
      '-la',
      'la',
      '--color=auto',
      'color',
      'grep',
      'permissions',
      'chmod permissions',
      'cmd:ls',
      'cmd:',
      'tag:starter',
      'cat:networking',
      'type:notes zebra',
      'ünïcode',
      'tcpdump',
      'dns',
      'permisions',
      'dirr',
      'nmap -sV',
      'file system',
      'status:completed',
      'sudo',
      'ssh key',
      'type:concepts questions',
      'bandit',
      'learned thing',
      'find size',
      'zzzzzz',
    ];
    const search = Object.fromEntries(
      queries.map((q) => [
        q,
        c2.search.search(q, { limit: 50 }).map((h) => ({
          type: h.type,
          id: h.id,
          title: h.title,
          score: round(h.score),
          highlight: h.highlight,
        })),
      ]),
    );
    const lists = Object.fromEntries(
      COLLECTIONS.map((c: CollectionName) => [
        c,
        queryEntities(c2.repos[c].all(), { limit: 500, archived: 'all' }).items.map((e) => e.id),
      ]),
    );
    const defaultLists = Object.fromEntries(
      COLLECTIONS.map((c: CollectionName) => [
        c,
        queryEntities(c2.repos[c].all(), { limit: 500 }).items.map((e) => e.title),
      ]),
    );
    const graph = c2.links.graph({ includeOrphans: true });
    const sampleIds = [
      cmdByName('ls').id,
      concepts[0]!.id,
      concepts[1]!.id,
      c2.repos.notes.all().find((n) => n.title === 'Parity hub')!.id,
    ];
    const linksFor = Object.fromEntries(
      sampleIds.map((id) => [id, { back: c2.links.backlinks(id), out: c2.links.outgoing(id) }]),
    );
    const queue = await c2.study.queue({ now: NOW, limit: 80 });
    const queue300 = await c2.study.queue({ now: NOW, limit: 80, tzOffset: 300 });
    const overview = await buildOverview(c2, { now: NOW });
    const overview300 = await buildOverview(c2, { now: NOW, tzOffset: 300 });
    return {
      now: NOW.toISOString(),
      search,
      lists,
      defaultLists,
      graph,
      links: linksFor,
      queue: {
        due: queue.due,
        newAvailable: queue.newAvailable,
        newToday: queue.newToday,
        items: queue.items,
      },
      queue300: {
        due: queue300.due,
        newAvailable: queue300.newAvailable,
        newToday: queue300.newToday,
        items: queue300.items,
      },
      stats: await c2.study.stats({ now: NOW }),
      questions: c2.study.questions(true),
      overview,
      overview300,
      scheduleSample: [0, 1, 2, 3, 4].map((n) => {
        let s = { ease: 2.5, intervalDays: 0, reps: 0, lapses: 0 } as ReturnType<typeof schedule>;
        const out = [];
        for (const rt of ['good', 'good', 'easy', 'hard', 'again', 'good', 'easy'].slice(n)) {
          s = schedule(s, rt as never, NOW);
          out.push(s);
        }
        return out;
      }),
    };
  } finally {
    await c2.close();
    rmSync(scratch, { recursive: true, force: true });
  }
}
