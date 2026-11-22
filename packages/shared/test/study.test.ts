import { describe, expect, it } from 'vitest';
import {
  addDays,
  availableCommandCards,
  buildCommandCard,
  buildConceptCard,
  buildRecommendations,
  checkFill,
  checkTask,
  commandsMatch,
  computeStreak,
  countOpen,
  dayKey,
  DEFAULT_SKILL_DOMAINS,
  domainsFor,
  isDue,
  masteryFromSrs,
  newSrs,
  parseQuestions,
  pickCommandCardType,
  schedule,
  scoreDomains,
  threatLevel,
  toggleQuestion,
  tokenizeCommand,
  weeklyMinutes,
  weekStart,
} from '../src/index.ts';

const T0 = new Date('2026-06-01T12:00:00.000Z');
const DAY = 86_400_000;

describe('SM-2 scheduler', () => {
  it('walks 1 → 6 → interval×ease on repeated "good"', () => {
    let s = schedule(newSrs(), 'good', T0);
    expect([s.intervalDays, s.reps, s.ease]).toEqual([1, 1, 2.5]);
    expect(Date.parse(s.dueAt!) - T0.getTime()).toBe(DAY);
    s = schedule(s, 'good', T0);
    expect(s.intervalDays).toBe(6);
    s = schedule(s, 'good', T0);
    expect(s.intervalDays).toBe(15); // 6 × 2.5
    s = schedule(s, 'good', T0);
    expect(s.intervalDays).toBe(38);
  });
  it('lapses reset repetitions, cost ease, and return within minutes', () => {
    const grown = schedule(schedule(schedule(newSrs(), 'good', T0), 'good', T0), 'good', T0);
    const s = schedule(grown, 'again', T0);
    expect([s.reps, s.lapses, s.intervalDays, s.ease]).toEqual([0, 1, 0, 2.3]);
    expect(Date.parse(s.dueAt!) - T0.getTime()).toBe(10 * 60_000);
    expect(isDue(s, new Date(T0.getTime() + 11 * 60_000))).toBe(true);
    expect(isDue(s, T0)).toBe(false);
  });
  it('hard grows slowly, easy faster, ease stays within [1.3, 3]', () => {
    const base = { ...schedule(schedule(newSrs(), 'good', T0), 'good', T0) }; // interval 6
    expect(schedule(base, 'hard', T0).intervalDays).toBe(7); // max(6+1, round(6×1.2))
    expect(schedule(base, 'easy', T0).intervalDays).toBeGreaterThan(
      schedule(base, 'good', T0).intervalDays,
    );
    let s = newSrs();
    for (let i = 0; i < 30; i++) s = schedule(s, 'again', T0);
    expect(s.ease).toBe(1.3);
    s = newSrs();
    for (let i = 0; i < 30; i++) s = schedule(s, 'easy', T0);
    expect(s.ease).toBe(3);
  });
  it('first easy review jumps to 3 days; second reviews differ by rating', () => {
    expect(schedule(newSrs(), 'easy', T0).intervalDays).toBe(3);
    const one = schedule(newSrs(), 'good', T0);
    expect(
      [schedule(one, 'hard', T0), schedule(one, 'good', T0), schedule(one, 'easy', T0)].map(
        (x) => x.intervalDays,
      ),
    ).toEqual([4, 6, 8]);
  });
  it('maps intervals to mastery', () => {
    expect(masteryFromSrs(newSrs())).toBe(0);
    expect(masteryFromSrs({ ...newSrs(), lastAt: 'x', intervalDays: 1 })).toBe(1);
    expect(masteryFromSrs({ ...newSrs(), lastAt: 'x', intervalDays: 6 })).toBe(2);
    expect(masteryFromSrs({ ...newSrs(), lastAt: 'x', intervalDays: 21 })).toBe(3);
  });
});

describe('typed answer checking', () => {
  it('ignores whitespace, flag order and clustering, leading $ and backticks', () => {
    expect(commandsMatch('ls   -a  -l ~', 'ls -la ~')).toBe(true);
    expect(commandsMatch('$ ls -al ~', 'ls -la ~')).toBe(true);
    expect(commandsMatch('`ls -al ~`', 'ls -la ~')).toBe(true);
    expect(commandsMatch('ls ~ -la', 'ls -la ~')).toBe(true);
  });
  it('rejects wrong command, flags, extra flags or reordered arguments', () => {
    expect(commandsMatch('ll -la ~', 'ls -la ~')).toBe(false);
    expect(commandsMatch('ls -l ~', 'ls -la ~')).toBe(false);
    expect(commandsMatch('ls -lah ~', 'ls -la ~')).toBe(false);
    expect(commandsMatch('cp b a', 'cp a b')).toBe(false);
    expect(commandsMatch('ls', 'ls ~')).toBe(false);
    expect(commandsMatch('', 'ls')).toBe(false);
  });
  it('treats long flags and quoted arguments exactly; placeholders match anything', () => {
    expect(commandsMatch('ls --color=auto', 'ls --color=auto')).toBe(true);
    expect(commandsMatch('ls --color', 'ls --color=auto')).toBe(false);
    expect(tokenizeCommand(`grep "two words" 'x y'`)).toEqual(['grep', 'two words', 'x y']);
    expect(commandsMatch('grep -i secret notes.txt', 'grep -i <pattern> [file]')).toBe(true);
    expect(commandsMatch('grep -i secret', 'grep -i <pattern> [file]')).toBe(false);
  });
  it('checkTask returns the matched reference; checkFill is exact after trimming', () => {
    expect(checkTask('rm -r  d', ['rm -rf d', 'rm -r d'])).toEqual({
      correct: true,
      matched: 'rm -r d',
    });
    expect(checkTask('rm d', ['rm -r d']).correct).toBe(false);
    expect(checkFill(' -a ', ['-a'])).toBe(true);
    expect(checkFill('-A', ['-a'])).toBe(false);
  });
});

const cmd = (o: object = {}) => ({
  id: 'c1',
  name: 'ls',
  title: 'ls',
  explanation: 'list entries',
  whatItDoes: 'Prints entries.',
  syntax: 'ls [flags]',
  mastery: 0,
  options: [
    { flag: '-a', meaning: 'include dotfiles' },
    { flag: 'cd ..', meaning: 'not a flag' },
  ],
  examples: [
    { command: 'ls -al ~', description: 'long list of home' },
    { command: 'ls', description: '' },
  ],
  ...o,
});

describe('card building', () => {
  it('builds each command card type from its data', () => {
    expect(buildCommandCard(cmd(), 'name')).toMatchObject({
      kind: 'reveal',
      prompt: 'What does `ls` do?',
      answer: 'list entries',
    });
    const task = buildCommandCard(cmd(), 'task')!;
    expect(task).toMatchObject({ kind: 'type', prompt: 'Type the command: long list of home' });
    expect(task.expected).toContain('ls -al ~');
    expect(buildCommandCard(cmd(), 'flag')).toMatchObject({
      prompt: 'In `ls`, what does `-a` do?',
      answer: 'include dotfiles',
    });
    const fill = buildCommandCard(cmd(), 'fill')!;
    expect(fill.kind).toBe('type');
    expect(fill.prompt).toBe('Fill in the blank: ls ____ ~');
    expect(fill.expected).toEqual(['-al']);
  });
  it('always blanks exactly one token in a fill card, whatever the id hashes to', () => {
    const many = {
      examples: [{ command: 'ls -a -l -h ~', description: 'long list' }],
    };
    for (let i = 0; i < 300; i++) {
      const fill = buildCommandCard(cmd({ ...many, id: `01HZ${i}ABCDEFG` }), 'fill', i % 5)!;
      expect(fill.prompt, `id ${i}`).toContain('____');
      expect(fill.expected?.[0], `id ${i}`).toMatch(/^-[alh]$/); // a flag, never undefined
    }
  });
  it('skips card types the data cannot support', () => {
    const bare = cmd({
      explanation: '',
      whatItDoes: '',
      options: [],
      examples: [{ command: 'ls', description: '' }],
    });
    expect(availableCommandCards(bare)).toEqual([]);
    expect(pickCommandCardType(bare, 0)).toBeUndefined();
    expect(buildCommandCard(bare, 'flag')).toBeUndefined();
    expect(availableCommandCards(cmd())).toEqual(['name', 'task', 'flag', 'fill']);
  });
  it('picks easier cards for new commands and harder ones as mastery grows', () => {
    expect(['name', 'task']).toContain(pickCommandCardType(cmd({ mastery: 0 }), 5));
    expect(['fill', 'task', 'flag']).toContain(pickCommandCardType(cmd({ mastery: 3 }), 7));
    expect(pickCommandCardType(cmd({ mastery: 2 }), 1)).toBe(
      pickCommandCardType(cmd({ mastery: 2 }), 1),
    );
  });
  it('builds concept cards from intuition or what-is-it, ignoring blank template bullets', () => {
    const base = { id: 'k', title: 'Wildcards' };
    expect(
      buildConceptCard({
        ...base,
        sections: [
          { key: 'intuition-very-important', markdown: '> "Like a shortcut"' },
          { key: 'what-is-it', markdown: '-' },
        ],
      }),
    ).toMatchObject({ type: 'teach', answer: '"Like a shortcut"' });
    expect(
      buildConceptCard({
        ...base,
        sections: [{ key: 'what-is-it', markdown: 'Patterns for file names' }],
      }),
    ).toMatchObject({ type: 'what', answer: 'Patterns for file names' });
    expect(
      buildConceptCard({ ...base, sections: [{ key: 'what-is-it', markdown: '- ' }] }),
    ).toBeUndefined();
  });
});

describe('streak, heatmap helpers, weekly minutes', () => {
  const days = (...d: string[]) => new Set(d);
  it('counts consecutive days, with a grace for today not yet practised', () => {
    expect(computeStreak(days('2026-06-01', '2026-05-31', '2026-05-30'), '2026-06-01')).toEqual({
      current: 3,
      longest: 3,
    });
    expect(computeStreak(days('2026-05-31', '2026-05-30'), '2026-06-01').current).toBe(2); // yesterday counts
    expect(computeStreak(days('2026-05-29'), '2026-06-01').current).toBe(0);
    expect(
      computeStreak(days('2026-05-01', '2026-05-02', '2026-05-03', '2026-05-10'), '2026-06-01')
        .longest,
    ).toBe(3);
    expect(computeStreak(new Set(), '2026-06-01')).toEqual({ current: 0, longest: 0 });
  });
  it('buckets timestamps into local days using the timezone offset', () => {
    expect(dayKey('2026-06-01T02:30:00Z', 0)).toBe('2026-06-01');
    expect(dayKey('2026-06-01T02:30:00Z', 300)).toBe('2026-05-31'); // UTC−5 evening
    expect(addDays('2026-02-28', 1)).toBe('2026-03-01');
    expect(weekStart('2026-06-03')).toBe('2026-06-01'); // Monday
    expect(weekStart('2026-06-07')).toBe('2026-06-01');
  });
  it('sums minutes into the last N Monday-based weeks with zeros included', () => {
    const w = weeklyMinutes(
      [
        { day: '2026-06-02', minutes: 30 },
        { day: '2026-06-03', minutes: 45 },
        { day: '2026-05-20', minutes: 10 },
        { day: '2025-01-01', minutes: 999 },
      ],
      '2026-06-05',
      4,
    );
    expect(w.map((x) => x.week)).toEqual(['2026-05-11', '2026-05-18', '2026-05-25', '2026-06-01']);
    expect(w.map((x) => x.minutes)).toEqual([0, 10, 0, 75]);
  });
});

describe('threat level', () => {
  it('is LOW when active recently and escalates with idle days and overdue cards', () => {
    expect(threatLevel({ daysSinceActivity: 0, streak: 5, overdue: 0 }).level).toBe('LOW');
    expect(threatLevel({ daysSinceActivity: 1, streak: 0, overdue: 4 }).level).toBe('LOW'); // 18
    expect(threatLevel({ daysSinceActivity: 2, streak: 0, overdue: 0 }).level).toBe('MODERATE');
    expect(threatLevel({ daysSinceActivity: 5, streak: 0, overdue: 0 }).level).toBe('ELEVATED');
    expect(threatLevel({ daysSinceActivity: 9, streak: 0, overdue: 0 }).level).toBe('HIGH');
    expect(threatLevel({ daysSinceActivity: null, streak: 0, overdue: 0 }).level).toBe('MODERATE'); // content but never practised
    expect(
      threatLevel({ daysSinceActivity: null, streak: 0, overdue: 0, empty: true }),
    ).toMatchObject({ level: 'LOW', score: 0 }); // brand-new profile
  });
  it('rewards streaks (capped) and never goes negative; explains itself', () => {
    expect(threatLevel({ daysSinceActivity: 2, streak: 14, overdue: 0 }).score).toBe(0);
    expect(threatLevel({ daysSinceActivity: 2, streak: 14, overdue: 0 }).level).toBe('LOW');
    const t = threatLevel({ daysSinceActivity: 3, streak: 0, overdue: 30 });
    expect(t.score).toBe(70); // 30 + 2×20 (overdue capped)
    expect(t.explanation).toContain('ELEVATED');
    expect(t.explanation).toContain('Score = 10×days idle');
  });
});

describe('skill map scoring', () => {
  const NOW = new Date('2026-06-10T00:00:00Z');
  const base = { archived: false, tags: [] as string[], updatedAt: '2026-06-09T00:00:00Z' };
  it('maps categories, tags and lab domains to domains via aliases', () => {
    expect(domainsFor(['File System'], DEFAULT_SKILL_DOMAINS)).toEqual(['Linux']);
    expect(domainsFor(['Text Processing'], DEFAULT_SKILL_DOMAINS)).toEqual(['Bash']);
    expect(domainsFor(['crypto'], DEFAULT_SKILL_DOMAINS)).toEqual(['Cryptography']);
    expect(domainsFor(['nonsense', ''], DEFAULT_SKILL_DOMAINS)).toEqual([]);
  });
  it('scores 0 for empty domains and rewards concepts, mastered commands and completed labs', () => {
    const data = {
      concepts: [
        { ...base, id: 'k1', category: 'Linux', difficulty: 'Hard', confidence: 3 },
        { ...base, id: 'k2', category: 'Linux', difficulty: 'Easy', confidence: 0 },
      ],
      commands: [
        {
          ...base,
          id: 'c1',
          category: 'File System',
          mastery: 3,
          lastPracticedAt: '2026-06-09T00:00:00Z',
        },
        { ...base, id: 'c2', category: 'Networking', mastery: 1 },
      ],
      labs: [
        {
          ...base,
          id: 'l1',
          category: 'Bandit',
          status: 'Completed',
          difficulty: 'Medium',
          domain: 'linux',
          conceptIds: [],
          solvedAt: '2026-06-09T00:00:00Z',
        },
        {
          ...base,
          id: 'l2',
          category: 'x',
          status: 'In Progress',
          difficulty: 'Hard',
          conceptIds: ['k1'],
        },
      ],
    };
    const s = Object.fromEntries(
      scoreDomains(DEFAULT_SKILL_DOMAINS, data, NOW).map((d) => [d.name, d]),
    );
    expect(s['Cryptography']!.score).toBe(0);
    expect(s['Linux']!.counts).toEqual({ concepts: 2, commands: 1, labs: 2 }); // l2 joins via its concept
    expect(s['Linux']!.parts.labs).toBeCloseTo(7.5, 1); // 30 × 2/8
    expect(s['Linux']!.parts.commands).toBeCloseTo(2.5, 1); // 25 × 1/10
    expect(s['Linux']!.parts.concepts).toBeCloseTo(35 * ((2 * 1 + 1 * 0.3) / 6), 0);
    expect(s['Linux']!.score).toBeGreaterThan(s['Networking']!.score);
    expect(s['Linux']!.score).toBeLessThanOrEqual(100);
  });
  it('decays the recency component over time', () => {
    const data = {
      concepts: [
        {
          ...base,
          id: 'k',
          category: 'Linux',
          difficulty: 'Easy',
          confidence: 3,
          updatedAt: '2026-06-09T00:00:00Z',
        },
      ],
      commands: [],
      labs: [],
    };
    const fresh = scoreDomains(DEFAULT_SKILL_DOMAINS, data, NOW).find((d) => d.name === 'Linux')!;
    const old = scoreDomains(DEFAULT_SKILL_DOMAINS, data, new Date(NOW.getTime() + 120 * DAY)).find(
      (d) => d.name === 'Linux',
    )!;
    expect(fresh.parts.recency).toBeGreaterThan(9);
    expect(old.parts.recency).toBeLessThan(0.3);
  });
});

describe('recommendations', () => {
  const NOW = new Date('2026-06-20T00:00:00Z');
  const domains = scoreDomains(
    DEFAULT_SKILL_DOMAINS,
    {
      concepts: [
        {
          id: 'k1',
          category: 'Networking',
          difficulty: 'Easy',
          confidence: 1,
          archived: false,
          tags: [],
          updatedAt: '2026-06-19T00:00:00Z',
        },
      ],
      commands: [],
      labs: [],
    },
    NOW,
  );
  const input = {
    domains,
    due: 3,
    now: NOW,
    concepts: [
      { id: 'k1', title: 'TCP', archived: false, questionsOpen: 2 },
      { id: 'k2', title: 'DNS', archived: false, questionsOpen: 0 },
    ],
    commands: [
      {
        id: 'c1',
        name: 'nmap',
        archived: false,
        mastery: 1,
        lastPracticedAt: '2026-05-01T00:00:00Z',
        createdAt: '2026-04-01T00:00:00Z',
      },
      { id: 'c2', name: 'ls', archived: false, mastery: 0, createdAt: '2026-06-19T00:00:00Z' },
      {
        id: 'c3',
        name: 'cd',
        archived: false,
        mastery: 3,
        lastPracticedAt: '2026-01-01T00:00:00Z',
        createdAt: '2026-01-01T00:00:00Z',
      },
    ],
    labs: [
      {
        id: 'l1',
        title: 'Bandit 3',
        archived: false,
        status: 'In Progress',
        conceptIds: ['k1'],
        updatedAt: '2026-06-18T00:00:00Z',
      },
    ],
  };
  it('orders by priority and always explains why', () => {
    const r = buildRecommendations(input);
    expect(r.map((x) => x.kind)).toEqual([
      'review',
      'weak-domain',
      'lab-in-progress',
      'concept-no-lab',
      'stale-command',
      'stale-command',
      'open-question',
    ]);
    expect(r.every((x) => x.why.length > 10)).toBe(true);
    expect(r[0]!.title).toBe('Review 3 due cards');
    expect(r.find((x) => x.kind === 'concept-no-lab')!.title).toContain('DNS'); // k1 is used by a lab
    expect(r.filter((x) => x.kind === 'stale-command').map((x) => x.title)).toEqual([
      'Revisit `ls`',
      'Revisit `nmap`',
    ]); // never-practised first; mastered cd excluded
    expect(r.find((x) => x.kind === 'weak-domain')!.title).toBe('Strengthen Networking');
  });
  it('is quiet when there is nothing to suggest', () => {
    expect(
      buildRecommendations({ domains: [], concepts: [], commands: [], labs: [], due: 0, now: NOW }),
    ).toEqual([]);
  });
});

describe('questions module', () => {
  it('parses, counts and toggles', () => {
    const md = '- a?\n- [x] b?\n-\n';
    expect(parseQuestions(md)).toHaveLength(2);
    expect(countOpen(md)).toBe(1);
    expect(toggleQuestion(md, 0).split('\n')[0]).toBe('- [x] a?');
  });
});
