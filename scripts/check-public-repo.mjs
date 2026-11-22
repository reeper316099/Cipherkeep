#!/usr/bin/env node
// Public-repo guard: fail if files that look like a personal Notion export are tracked/staged.
// Usage: node scripts/check-public-repo.mjs [--staged]   (default: all tracked files)
import { execFileSync } from 'node:child_process';
import { readFileSync, statSync } from 'node:fs';

const ALLOWED_PREFIXES = ['fixtures/synthetic/', 'fixtures/compat/', 'starter-pack/'];
const NOTION_ID = /[0-9a-f]{32}(_all)?\.(md|csv)$/i;

/** Returns a list of human-readable violations for the given repo-relative paths. */
export function findViolations(paths) {
  const out = [];
  for (const p of paths) {
    const norm = p.replace(/\\/g, '/');
    if (ALLOWED_PREFIXES.some((a) => norm.startsWith(a))) continue;
    if (norm.startsWith('private-fixtures/'))
      out.push(`${norm}: private-fixtures must never be committed`);
    else if (/\.(zip|csv)$/i.test(norm))
      out.push(`${norm}: .zip/.csv only allowed in fixtures/synthetic or starter-pack`);
    else if (NOTION_ID.test(norm))
      out.push(`${norm}: looks like a Notion export page (32-hex id suffix)`);
  }
  return out;
}

const SECRET_PATTERNS = [
  ['AWS access key', /\bAKIA[0-9A-Z]{16}\b/],
  ['GitHub token', /\bgh[pousr]_[A-Za-z0-9]{30,}\b/],
  ['Notion token', /\b(?:ntn_|secret_)[A-Za-z0-9]{30,}\b/],
  ['API secret key (sk-...)', /\bsk-[A-Za-z0-9_-]{24,}\b/],
  ['Slack token', /\bxox[abprs]-[A-Za-z0-9-]{10,}\b/],
  ['Google API key', /\bAIza[0-9A-Za-z_-]{35}\b/],
  ['private key block', new RegExp('-----BEGIN [A-Z ]*PRIVATE KEY-----\\s*[A-Za-z0-9+/=\\s]{60,}')],
];

/** Returns violations for text that looks like it contains a real credential. */
export function findSecrets(file, text) {
  const out = [];
  for (const [label, re] of SECRET_PATTERNS)
    if (re.test(text)) out.push(`${file}: looks like it contains a ${label}`);
  return out;
}

function scanContents(files) {
  const out = [];
  for (const f of files) {
    try {
      if (statSync(f).size > 2_000_000) continue;
      const buf = readFileSync(f);
      if (buf.includes(0)) continue; // binary
      out.push(...findSecrets(f, buf.toString('utf8')));
    } catch {
      /* deleted or unreadable: nothing to scan */
    }
  }
  return out;
}

function main() {
  const staged = process.argv.includes('--staged');
  const args = staged ? ['diff', '--cached', '--name-only', '--diff-filter=ACMR'] : ['ls-files'];
  const files = execFileSync('git', args, { encoding: 'utf8' }).split('\n').filter(Boolean);
  const violations = [...findViolations(files), ...scanContents(files)];
  if (violations.length) {
    console.error('Public-repo guard failed:\n' + violations.map((v) => '  - ' + v).join('\n'));
    process.exit(1);
  }
  console.log(`Public-repo guard OK (${files.length} files checked).`);
}

import { fileURLToPath } from 'node:url';
if (process.argv[1] === fileURLToPath(import.meta.url)) main();
