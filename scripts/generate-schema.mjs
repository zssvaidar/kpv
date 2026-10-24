#!/usr/bin/env node
// Generates every artifact derived from schema/pipeline.schema.json.
//
// The JSON Schema is the single source of truth. Nothing below is hand-written
// afterwards: `pnpm run generate` rewrites all of it, and `pnpm run
// generate:check` (which CI runs) regenerates and fails when the committed
// output differs, which is also what catches a hand-edit.
//
// Generators, pinned so the step is reproducible:
//   - json-schema-to-typescript  -> TypeScript interfaces  (root devDependency)
//   - json-schema-to-zod         -> Zod schemas            (root devDependency)
//   - go-jsonschema              -> Go structs             (tool directive in agent/go.mod)
//
// Two notes on the generated Zod:
//   - a `default` becomes `.default(...)`, so parsing fills the value in. That
//     makes a defaulted property optional on input, which is only the same
//     thing the JSON Schema says while no required property carries a default;
//     the check below refuses a schema where one does, because that is exactly
//     where Zod and a JSON Schema validator would start disagreeing.
//   - every `$ref` becomes a reference to the matching generated constant
//     instead of an inlined copy, which keeps one named schema per definition.

import { execFileSync } from 'node:child_process';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

import { compile } from 'json-schema-to-typescript';
import { jsonSchemaToZod } from 'json-schema-to-zod';
import { format, resolveConfig } from 'prettier';

const repoRoot = dirname(dirname(fileURLToPath(import.meta.url)));
const schemaPath = join(repoRoot, 'schema', 'pipeline.schema.json');
const tsOutDir = join(repoRoot, 'packages', 'schema', 'src', 'generated');
const goOutDir = join(repoRoot, 'agent', 'internal', 'pipeline');

const schemaSource = readFileSync(schemaPath, 'utf8');
/** @type {Record<string, any>} */
const schema = JSON.parse(schemaSource);
/** @type {Record<string, any>} */
const defs = schema.$defs ?? {};

const banner = (relativeSelf) =>
  [
    `// Code generated from schema/pipeline.schema.json by scripts/generate-schema.mjs.`,
    `// DO NOT EDIT. Run \`pnpm run generate\` instead; CI fails when ${relativeSelf} is stale.`,
    '',
  ].join('\n');

// --- helpers over the schema --------------------------------------------------

const defNameOf = (ref) => {
  const match = /^#\/\$defs\/([^/]+)$/.exec(ref);
  if (match === null) {
    throw new Error(
      `only local #/$defs/<name> references are supported, got ${ref}`,
    );
  }
  const name = match[1];
  if (!(name in defs)) {
    throw new Error(`dangling reference ${ref}`);
  }
  return name;
};

const constNameOf = (defName) => `${defName}Schema`;

/** Every #/$defs/<name> reached from a node, however deeply nested. */
const referencedDefs = (node, found = new Set()) => {
  if (Array.isArray(node)) {
    for (const item of node) referencedDefs(item, found);
    return found;
  }
  if (node === null || typeof node !== 'object') return found;
  for (const [key, value] of Object.entries(node)) {
    if (key === '$ref' && typeof value === 'string') {
      found.add(defNameOf(value));
      continue;
    }
    referencedDefs(value, found);
  }
  return found;
};

/** Definitions ordered so a constant is always declared before it is used. */
const orderedDefNames = () => {
  const ordered = [];
  const state = new Map();
  const visit = (name, trail) => {
    const seen = state.get(name);
    if (seen === 'done') return;
    if (seen === 'visiting') {
      throw new Error(`cycle through $defs: ${[...trail, name].join(' -> ')}`);
    }
    state.set(name, 'visiting');
    for (const dep of referencedDefs(defs[name])) {
      visit(dep, [...trail, name]);
    }
    state.set(name, 'done');
    ordered.push(name);
  };
  for (const name of Object.keys(defs)) visit(name, []);
  return ordered;
};

/**
 * Recognizes `oneOf` over references whose branches all pin the same property
 * to a single value, which is what makes the union discriminated. Returns the
 * discriminator and the branch definition names, or null.
 */
const discriminatedUnionOf = (node) => {
  if (!Array.isArray(node.oneOf) || node.oneOf.length < 2) return null;
  const branches = [];
  let property = null;
  for (const branch of node.oneOf) {
    if (
      branch === null ||
      typeof branch !== 'object' ||
      Object.keys(branch).length !== 1 ||
      typeof branch.$ref !== 'string'
    ) {
      return null;
    }
    const branchName = defNameOf(branch.$ref);
    const target = defs[branchName];
    const discriminators = Object.entries(target.properties ?? {}).filter(
      ([name, subSchema]) =>
        Array.isArray(subSchema.enum) &&
        subSchema.enum.length === 1 &&
        (target.required ?? []).includes(name),
    );
    if (discriminators.length !== 1) return null;
    const [name] = discriminators[0];
    if (property === null) property = name;
    else if (property !== name) return null;
    branches.push(branchName);
  }
  return { property, branches };
};

/**
 * A required property with a `default` would be optional in the generated Zod
 * and required in the schema, so the two sides would disagree about a document
 * that leaves it out. Refuse it instead of generating the disagreement.
 */
const assertNoDefaultOnRequired = (node, path) => {
  if (Array.isArray(node)) {
    node.forEach((item, index) => {
      assertNoDefaultOnRequired(item, `${path}/${index}`);
    });
    return;
  }
  if (node === null || typeof node !== 'object') return;
  const required = Array.isArray(node.required) ? node.required : [];
  for (const [name, sub] of Object.entries(node.properties ?? {})) {
    if (
      required.includes(name) &&
      sub !== null &&
      typeof sub === 'object' &&
      'default' in sub
    ) {
      throw new Error(
        `${path}/properties/${name} is required and has a default; ` +
          'a required property must not carry one',
      );
    }
  }
  for (const [key, value] of Object.entries(node)) {
    assertNoDefaultOnRequired(value, `${path}/${key}`);
  }
};

// --- Zod ---------------------------------------------------------------------

const parserOverride = (node) =>
  typeof node?.$ref === 'string'
    ? constNameOf(defNameOf(node.$ref))
    : undefined;

const zodExpression = (node) =>
  jsonSchemaToZod(node, {
    module: 'none',
    parserOverride,
  });

const zodModule = () => {
  const parts = [
    banner('packages/schema/src/generated/pipeline-zod.ts'),
    `import { z } from 'zod';`,
    '',
  ];
  for (const name of orderedDefNames()) {
    const node = defs[name];
    const union = discriminatedUnionOf(node);
    let expression;
    if (union === null) {
      expression = zodExpression(node);
    } else {
      const members = union.branches.map(constNameOf).join(', ');
      expression = `z.discriminatedUnion(${JSON.stringify(union.property)}, [${members}])`;
      if (typeof node.description === 'string') {
        expression += `.describe(${JSON.stringify(node.description)})`;
      }
    }
    parts.push(`export const ${constNameOf(name)} = ${expression};`, '');
  }
  const root = { ...schema };
  delete root.$schema;
  delete root.$id;
  delete root.$defs;
  parts.push(`export const pipelineSchema = ${zodExpression(root)};`, '');
  return parts.join('\n');
};

// --- raw schema as a TypeScript module ---------------------------------------

const rawSchemaModule = () =>
  [
    banner('packages/schema/src/generated/pipeline-json-schema.ts'),
    '/** The authoritative JSON Schema, verbatim, for JSON Schema validators. */',
    'export const pipelineJsonSchema: Readonly<Record<string, unknown>> =',
    `  ${JSON.stringify(schema, null, 2)};`,
    '',
  ].join('\n');

// --- writing -----------------------------------------------------------------

const prettierFor = async (filePath, source) => {
  const config = await resolveConfig(filePath);
  return format(source, { ...config, filepath: filePath });
};

const written = [];

const writeFormatted = async (filePath, source) => {
  mkdirSync(dirname(filePath), { recursive: true });
  writeFileSync(filePath, await prettierFor(filePath, source));
  written.push(filePath);
};

const main = async () => {
  assertNoDefaultOnRequired(schema, '');

  await writeFormatted(
    join(tsOutDir, 'pipeline-types.ts'),
    banner('packages/schema/src/generated/pipeline-types.ts') +
      (await compile(schema, 'Pipeline', {
        bannerComment: '',
        additionalProperties: false,
        declareExternallyReferenced: true,
        enableConstEnums: false,
        format: false,
        unknownAny: true,
      })),
  );
  await writeFormatted(join(tsOutDir, 'pipeline-zod.ts'), zodModule());
  await writeFormatted(
    join(tsOutDir, 'pipeline-json-schema.ts'),
    rawSchemaModule(),
  );

  // The Go module cannot embed a file outside itself, so the schema is copied
  // in verbatim and embedded from there. The staleness check keeps the copy
  // honest.
  const goSchemaCopy = join(goOutDir, 'pipeline.schema.json');
  mkdirSync(dirname(goSchemaCopy), { recursive: true });
  writeFileSync(goSchemaCopy, schemaSource);
  written.push(goSchemaCopy);

  const goModels = join(goOutDir, 'models_gen.go');
  execFileSync(
    'go',
    [
      'tool',
      'go-jsonschema',
      '--package',
      'pipeline',
      '--tags',
      'json,yaml',
      '--struct-name-from-title',
      '--capitalization',
      'ID',
      '--output',
      goModels,
      schemaPath,
    ],
    { cwd: join(repoRoot, 'agent'), stdio: 'inherit' },
  );
  written.push(goModels);

  for (const filePath of written) {
    process.stdout.write(`generated ${filePath.slice(repoRoot.length + 1)}\n`);
  }
};

await main();
