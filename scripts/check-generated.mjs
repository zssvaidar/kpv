#!/usr/bin/env node
// Fails when anything generated from a schema in schema/ is stale.
//
// The generated output is committed, so this check is simply: regenerate, then
// ask git whether anything moved. That is what makes it catch both a schema
// change nobody regenerated for and a hand-edit of a generated file, since
// regenerating overwrites the edit and the diff shows up here. The schemas
// themselves are listed too, because the Go module holds a committed copy of
// each one and a schema edit that nobody regenerated for shows up as a diff
// between the two.

import { execFileSync } from 'node:child_process';
import { dirname } from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

const repoRoot = dirname(dirname(fileURLToPath(import.meta.url)));

// Everything `pnpm run generate` writes, plus the schema it reads: a diff in
// any of them means the committed tree and the schema disagree.
const generatedPaths = [
  'schema/pipeline.schema.json',
  'schema/channel.schema.json',
  'schema/directory.schema.json',
  'schema/roots.schema.json',
  'packages/schema/src/generated',
  'agent/internal/pipeline/models_gen.go',
  'agent/internal/pipeline/pipeline.schema.json',
  'agent/internal/channel/models_gen.go',
  'agent/internal/channel/channel.schema.json',
  'agent/internal/httpapi/models_gen.go',
  'agent/internal/httpapi/directory.schema.json',
  'agent/internal/httpapi/roots_models_gen.go',
  'agent/internal/httpapi/roots.schema.json',
];

const git = (...args) =>
  execFileSync('git', args, { cwd: repoRoot, encoding: 'utf8' });

execFileSync('node', ['scripts/generate-schema.mjs'], {
  cwd: repoRoot,
  stdio: 'inherit',
});

const dirty = git(
  'status',
  '--porcelain',
  '--untracked-files=all',
  '--',
  ...generatedPaths,
).trim();

if (dirty === '') {
  process.stdout.write('generated output is up to date with the schema\n');
  process.exit(0);
}

process.stderr.write(
  [
    'Generated output is stale or was hand-edited.',
    '',
    'These paths are generated from the schemas in schema/ and differ from',
    'what is committed:',
    '',
    dirty,
    '',
    'Run `pnpm run generate` and commit the result. Never edit generated files',
    'by hand: regenerating overwrites the edit, which is what this check sees.',
    '',
  ].join('\n'),
);
process.stderr.write(git('diff', '--', ...generatedPaths));
process.exit(1);
