export type CardType = 'name' | 'task' | 'flag' | 'fill' | 'teach' | 'what';

export interface Card {
  entityId: string;
  collection: 'commands' | 'concepts';
  type: CardType;
  /** Title of the entity (shown small above the prompt). */
  title: string;
  prompt: string;
  /** `reveal`: flip and self-rate. `type`: the learner types an answer that is checked. */
  kind: 'reveal' | 'type';
  /** What is shown after answering. */
  answer: string;
  /** Accepted typed answers (for `kind: 'type'`). */
  expected?: string[];
  hint?: string;
}

/* ---------- typed-answer checking ---------- */

const PLACEHOLDER_TOKEN = /^(?:<[^>]+>|\{[^}]+\}|\[[A-Za-z][\w -]*\])$/;

export function normalizeCommandText(s: string): string {
  return s
    .trim()
    .replace(/^`+|`+$/g, '')
    .replace(/^[$#]\s+/, '')
    .trim();
}

/** Whitespace tokens; single/double-quoted spans stay one token (quotes stripped). */
export function tokenizeCommand(s: string): string[] {
  const text = normalizeCommandText(s);
  return [...text.matchAll(/"([^"]*)"|'([^']*)'|(\S+)/g)].map((m) => m[1] ?? m[2] ?? m[3]!);
}

interface Parsed {
  cmd: string;
  shorts: Set<string>;
  longs: Set<string>;
  positional: string[];
}

function parse(s: string): Parsed {
  const [cmd = '', ...rest] = tokenizeCommand(s);
  const shorts = new Set<string>();
  const longs = new Set<string>();
  const positional: string[] = [];
  let doubleDash = false;
  for (const t of rest) {
    if (doubleDash) positional.push(t);
    else if (t === '--') doubleDash = true;
    else if (t.startsWith('--')) longs.add(t);
    else if (/^-[A-Za-z0-9]+$/.test(t))
      for (const ch of t.slice(1)) shorts.add(ch); // -la → l, a
    else positional.push(t);
  }
  return { cmd, shorts, longs, positional };
}

const sameSet = (a: Set<string>, b: Set<string>) =>
  a.size === b.size && [...a].every((x) => b.has(x));

/**
 * Does `input` express the same command as `reference`? Tolerates whitespace, flag order and clustering
 * (`-la` = `-l -a`), a leading `$`, and reference placeholders (`<file>`, `[dir]`) matching any value.
 * Positional arguments must keep their order.
 */
export function commandsMatch(input: string, reference: string): boolean {
  const a = parse(input);
  const r = parse(reference);
  if (!a.cmd || a.cmd !== r.cmd) return false;
  if (!sameSet(a.shorts, r.shorts) || !sameSet(a.longs, r.longs)) return false;
  if (a.positional.length !== r.positional.length) return false;
  return r.positional.every((p, i) => PLACEHOLDER_TOKEN.test(p) || p === a.positional[i]);
}

export function checkTask(
  input: string,
  expected: string[],
): { correct: boolean; matched?: string } {
  const hit = expected.find((e) => commandsMatch(input, e));
  return hit === undefined ? { correct: false } : { correct: true, matched: hit };
}

export const checkFill = (input: string, expected: string[]) => expected.includes(input.trim());

/* ---------- card building ---------- */

interface CmdLike {
  id: string;
  name: string;
  title: string;
  explanation: string;
  whatItDoes: string;
  syntax: string;
  mastery: number;
  options: { flag: string; meaning: string }[];
  examples: { command: string; description: string }[];
}
interface ConceptLike {
  id: string;
  title: string;
  sections: { key: string; markdown: string }[];
}

const hash = (s: string) => {
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) h = Math.imul(h ^ s.charCodeAt(i), 16777619);
  return h >>> 0;
};
const pick = <T>(xs: T[], seed: number): T | undefined =>
  xs.length ? xs[seed % xs.length] : undefined;
const blank = (md: string) => md.replace(/^\s*(?:[-*+]|\d+\.)\s*$/gm, '').trim() === '';

export function availableCommandCards(c: CmdLike): CardType[] {
  const out: CardType[] = [];
  if (c.explanation.trim() || c.whatItDoes.trim()) out.push('name');
  const describable = c.examples.some((e) => e.description.trim()) || c.explanation.trim();
  if (c.examples.length && describable) out.push('task');
  if (c.options.some((o) => /^--?[A-Za-z]/.test(o.flag) && o.meaning.trim())) out.push('flag');
  if (c.examples.some((e) => fillCandidates(e.command).length)) out.push('fill');
  return out;
}

const ORDER_BY_MASTERY: CardType[][] = [
  ['name', 'task'],
  ['name', 'task', 'flag'],
  ['task', 'flag', 'fill', 'name'],
  ['fill', 'task', 'flag'],
];

/** Chooses a card type suited to how well the command is known, rotating with `seed`. */
export function pickCommandCardType(c: CmdLike, seed: number): CardType | undefined {
  const avail = availableCommandCards(c);
  if (!avail.length) return undefined;
  const pref = (ORDER_BY_MASTERY[c.mastery] ?? ORDER_BY_MASTERY[0]!).filter((t) =>
    avail.includes(t),
  );
  return pick(pref.length ? pref : avail, seed);
}

/** Token indexes that can be blanked: flags and arguments, never the command itself. */
function fillCandidates(command: string): number[] {
  const toks = tokenizeCommand(command);
  return toks.map((_, i) => i).filter((i) => i > 0 && !PLACEHOLDER_TOKEN.test(toks[i]!));
}

export function buildCommandCard(c: CmdLike, type: CardType, seed = 0): Card | undefined {
  const base = { entityId: c.id, collection: 'commands' as const, title: c.name, type };
  const s = hash(c.id) + seed;
  switch (type) {
    case 'name': {
      const answer = c.explanation.trim() || c.whatItDoes.trim();
      return answer
        ? { ...base, kind: 'reveal', prompt: `What does \`${c.name}\` do?`, answer }
        : undefined;
    }
    case 'task': {
      const described = c.examples.filter((e) => e.description.trim());
      const ex = pick(described, s) ?? (c.explanation.trim() ? c.examples[0] : undefined);
      if (!ex) return undefined;
      const what = ex.description.trim() || c.explanation.trim();
      const same = c.examples
        .filter((e) => e.description.trim() === ex.description.trim() || e === ex)
        .map((e) => e.command);
      return {
        ...base,
        kind: 'type',
        prompt: `Type the command: ${what}`,
        answer: ex.command,
        expected: [...new Set([ex.command, ...same])],
        hint: `starts with “${c.name}”`,
      };
    }
    case 'flag': {
      const flags = c.options.filter((o) => /^--?[A-Za-z]/.test(o.flag) && o.meaning.trim());
      const o = pick(flags, s);
      return o
        ? {
            ...base,
            kind: 'reveal',
            prompt: `In \`${c.name}\`, what does \`${o.flag}\` do?`,
            answer: o.meaning,
          }
        : undefined;
    }
    case 'fill': {
      const cands = c.examples.filter((e) => fillCandidates(e.command).length);
      const ex = pick(cands, s);
      if (!ex) return undefined;
      const toks = tokenizeCommand(ex.command);
      const idxs = fillCandidates(ex.command);
      // prefer flags: they are what you forget
      const flagIdx = idxs.filter((i) => toks[i]!.startsWith('-'));
      // `>>>`, not `>>`: a signed shift turns the seed negative for about half of all ids, `xs[negative]` is
      // undefined, and the card would have no blank and no accepted answer.
      const i = pick(flagIdx.length ? flagIdx : idxs, s >>> 3)!;
      const shown = toks.map((t, k) => (k === i ? '____' : t)).join(' ');
      return {
        ...base,
        kind: 'type',
        prompt: `Fill in the blank: ${shown}`,
        answer: ex.command,
        expected: [toks[i]!],
        hint: ex.description || undefined,
      };
    }
    default:
      return undefined;
  }
}

export function buildConceptCard(c: ConceptLike, seed = 0): Card | undefined {
  const sec = (key: string) => c.sections.find((s) => s.key === key)?.markdown ?? '';
  const intuition = sec('intuition-very-important');
  const what = sec('what-is-it');
  const base = {
    entityId: c.id,
    collection: 'concepts' as const,
    title: c.title,
    kind: 'reveal' as const,
  };
  const useTeach = !blank(intuition) && (blank(what) || (hash(c.id) + seed) % 2 === 0);
  if (useTeach)
    return {
      ...base,
      type: 'teach',
      prompt: `Explain “${c.title}” in your own words. What is it basically like…?`,
      answer: intuition.replace(/^>\s?/gm, '').trim(),
    };
  if (!blank(what)) return { ...base, type: 'what', prompt: `What is “${c.title}”?`, answer: what };
  return undefined;
}
