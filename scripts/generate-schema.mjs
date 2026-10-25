#!/usr/bin/env node
// Generates every artifact derived from the JSON Schemas in schema/.
//
// A JSON Schema is the single source of truth for the model it describes.
// Nothing below is hand-written afterwards: `pnpm run generate` rewrites all of
// it, and `pnpm run generate:check` (which CI runs) regenerates and fails when
// the committed output differs, which is also what catches a hand-edit.
//
// There are four schemas, and they are separate files because they describe
// separate things:
//   - schema/pipeline.schema.json   the pipeline model
//   - schema/channel.schema.json    the envelope the agent pushes over the
//                                   WebSocket channel (#5), which carries a
//                                   pipeline id but is not a pipeline
//   - schema/directory.schema.json  what the directory browsing endpoint
//                                   answers (#7), a listing or an error
//   - schema/roots.schema.json      what the project root endpoints take and
//                                   answer (#63): the root list, a request,
//                                   an authorized root or an error
// Each one generates the same four artifacts: TypeScript types, Zod schemas,
// the raw schema as a module, and Go structs plus a copy of the schema the Go
// module can embed.
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
const tsOutDir = join(repoRoot, 'packages', 'schema', 'src', 'generated');

/**
 * Every schema and where its output goes. `prefix` names the TypeScript files
 * and the raw-schema constant; `rootName` is the root type on both sides, and
 * the Zod constant for the root is `<camelCase rootName>Schema`, which is how
 * `pipelineSchema` keeps the name it has always had.
 */
const schemas = [
  {
    file: 'pipeline.schema.json',
    prefix: 'pipeline',
    rootName: 'Pipeline',
    rootConst: 'pipelineSchema',
    rawConst: 'pipelineJsonSchema',
    goPackage: 'pipeline',
    goDir: join('agent', 'internal', 'pipeline'),
  },
  {
    file: 'channel.schema.json',
    prefix: 'channel',
    rootName: 'ChannelMessage',
    rootConst: 'channelMessageSchema',
    rawConst: 'channelJsonSchema',
    goPackage: 'channel',
    goDir: join('agent', 'internal', 'channel'),
  },
  {
    file: 'directory.schema.json',
    prefix: 'directory',
    rootName: 'DirectoryListing',
    rootConst: 'directoryListingSchema',
    rawConst: 'directoryJsonSchema',
    goPackage: 'httpapi',
    goDir: join('agent', 'internal', 'httpapi'),
    // The error answer is a definition nothing in the listing refers to, and
    // a client needs its type as much as the listing's, so the TypeScript
    // side is generated for every definition rather than only the reachable
    // ones.
    unreachableDefinitions: true,
  },
  {
    file: 'roots.schema.json',
    prefix: 'roots',
    rootName: 'RootList',
    rootConst: 'rootListSchema',
    rawConst: 'rootsJsonSchema',
    goPackage: 'httpapi',
    goDir: join('agent', 'internal', 'httpapi'),
    // httpapi already holds the directory schema's models_gen.go, so this one
    // gets a file of its own beside it.
    goFile: 'roots_models_gen.go',
    // The request, the authorized answer and the error are definitions the
    // list does not refer to, and a client needs every one of them.
    unreachableDefinitions: true,
  },
];

const banner = (schemaFile, relativeSelf) =>
  [
    `// Code generated from schema/${schemaFile} by scripts/generate-schema.mjs.`,
    `// DO NOT EDIT. Run \`pnpm run generate\` instead; CI fails when ${relativeSelf} is stale.`,
    '',
  ].join('\n');

// --- helpers over one schema -------------------------------------------------

/**
 * The helpers that need to see one schema's `$defs`, bound to that schema.
 * They are a closure rather than top-level functions so that two schemas
 * cannot see each other's definitions: a `$ref` is local by construction.
 */
const helpersFor = (schema) => {
  const defs = schema.$defs ?? {};

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
        throw new Error(
          `cycle through $defs: ${[...trail, name].join(' -> ')}`,
        );
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

  return {
    defs,
    defNameOf,
    constNameOf,
    orderedDefNames,
    discriminatedUnionOf,
  };
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

/**
 * The channel envelope carries a pipeline id, and the pipeline model defines
 * what an identifier is. The two files cannot share a `$ref` (a reference is
 * local, which is what keeps each schema self-contained and embeddable), so the
 * pattern is written twice and this is what keeps the copy honest: a change to
 * the identifier pattern on either side fails generation instead of quietly
 * letting the channel accept an id the model rejects.
 */
const assertThePipelineIDPatternMatches = (byFile) => {
  const identifier = byFile['pipeline.schema.json']?.$defs?.identifier?.pattern;
  const carried = byFile['channel.schema.json']?.$defs?.pipelineId?.pattern;
  if (identifier === undefined || carried === undefined) {
    throw new Error(
      'the identifier pattern or the channel pipelineId pattern is gone, so nothing is holding the two schemas to the same ids',
    );
  }
  if (identifier !== carried) {
    throw new Error(
      [
        'the channel envelope and the pipeline model disagree about what a pipeline id is:',
        `  schema/pipeline.schema.json $defs/identifier/pattern  ${identifier}`,
        `  schema/channel.schema.json  $defs/pipelineId/pattern   ${carried}`,
        'Make them the same, so a message cannot name an id no pipeline can have.',
      ].join('\n'),
    );
  }
};

// --- Zod ---------------------------------------------------------------------

const zodModuleFor = (schema, spec) => {
  const {
    defs,
    defNameOf,
    constNameOf,
    orderedDefNames,
    discriminatedUnionOf,
  } = helpersFor(schema);

  const parserOverride = (node) =>
    typeof node?.$ref === 'string'
      ? constNameOf(defNameOf(node.$ref))
      : undefined;

  const zodExpression = (node) =>
    jsonSchemaToZod(node, { module: 'none', parserOverride });

  const parts = [
    banner(spec.file, `packages/schema/src/generated/${spec.prefix}-zod.ts`),
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
  parts.push(`export const ${spec.rootConst} = ${zodExpression(root)};`, '');
  return parts.join('\n');
};

// --- raw schema as a TypeScript module ---------------------------------------

const rawSchemaModuleFor = (schema, spec) =>
  [
    banner(
      spec.file,
      `packages/schema/src/generated/${spec.prefix}-json-schema.ts`,
    ),
    '/** The authoritative JSON Schema, verbatim, for JSON Schema validators. */',
    `export const ${spec.rawConst}: Readonly<Record<string, unknown>> =`,
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

const generate = async (spec, schema, schemaSource) => {
  const schemaPath = join(repoRoot, 'schema', spec.file);

  await writeFormatted(
    join(tsOutDir, `${spec.prefix}-types.ts`),
    banner(spec.file, `packages/schema/src/generated/${spec.prefix}-types.ts`) +
      (await compile(schema, spec.rootName, {
        bannerComment: '',
        additionalProperties: false,
        declareExternallyReferenced: true,
        enableConstEnums: false,
        format: false,
        unknownAny: true,
        unreachableDefinitions: spec.unreachableDefinitions === true,
      })),
  );
  await writeFormatted(
    join(tsOutDir, `${spec.prefix}-zod.ts`),
    zodModuleFor(schema, spec),
  );
  await writeFormatted(
    join(tsOutDir, `${spec.prefix}-json-schema.ts`),
    rawSchemaModuleFor(schema, spec),
  );

  // The Go module cannot embed a file outside itself, so the schema is copied
  // in verbatim and embedded from there. The staleness check keeps the copy
  // honest.
  const goDir = join(repoRoot, spec.goDir);
  const goSchemaCopy = join(goDir, spec.file);
  mkdirSync(goDir, { recursive: true });
  writeFileSync(goSchemaCopy, schemaSource);
  written.push(goSchemaCopy);

  const goModels = join(goDir, spec.goFile ?? 'models_gen.go');
  execFileSync(
    'go',
    [
      'tool',
      'go-jsonschema',
      '--package',
      spec.goPackage,
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
};

const main = async () => {
  // Everything is read and checked before anything is written, so a schema
  // that fails a check leaves the committed output alone.
  const sources = schemas.map((spec) => {
    const source = readFileSync(join(repoRoot, 'schema', spec.file), 'utf8');
    return { spec, source, schema: JSON.parse(source) };
  });
  const byFile = Object.fromEntries(
    sources.map(({ spec, schema }) => [spec.file, schema]),
  );
  for (const { spec, schema } of sources) {
    assertNoDefaultOnRequired(schema, `schema/${spec.file}`);
  }
  assertThePipelineIDPatternMatches(byFile);

  for (const { spec, schema, source } of sources) {
    await generate(spec, schema, source);
  }

  for (const filePath of written) {
    process.stdout.write(`generated ${filePath.slice(repoRoot.length + 1)}\n`);
  }
};

await main();
