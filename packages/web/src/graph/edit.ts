// The graph's editing operations as pure transforms of the pipeline model:
// insert a processing step between two blocks, move a step to another place
// in the chain, delete a step.
//
// Each takes a pipeline and returns a result. It never changes the pipeline
// it is handed or anything in it: a successful operation builds a new
// pipeline, checks it against the generated Zod schema in @kpv/schema (with
// the schema's annotation rules, so a duplicate step id fails too), and only
// then returns it. Anything else, whether the operation makes no sense (moving
// the source, deleting a step that is not there) or its outcome fails the
// schema, is a rejection that carries the previous pipeline back as it was.
//
// Nothing here imports React or React Flow, so the graph (PipelineGraph.tsx)
// and the side panel (EditControls.tsx) are only the places these are called
// from, and every operation is tested here without either.

import type {
  CustomCodeStep,
  FilterStep,
  Pipeline,
  ProcessingStep,
  TransformStep,
  ValidateStep,
} from '@kpv/schema';
import { pipelineZodSchema } from '@kpv/schema';

/**
 * One block of a pipeline, by where it sits: the source, the destination, or
 * a processing step by its id. A step's id is unique among the steps, which
 * is what lets it name one.
 */
export type BlockRef =
  { role: 'source' } | { role: 'destination' } | { role: 'step'; id: string };

/** The type of a processing step. */
export type StepType = ProcessingStep['type'];

/** Every processing step type, in the order a menu offers them. */
export const stepTypes = [
  'filter',
  'transform',
  'validate',
  'customCode',
] as const satisfies readonly StepType[];

/**
 * What an operation gives back. On success, the new pipeline, and for an
 * insert the id the new step was given. On rejection, the pipeline it was
 * handed, the very same object, and why.
 */
export type EditResult =
  | { ok: true; pipeline: Pipeline; stepId?: string }
  | { ok: false; pipeline: Pipeline; reason: string };

/**
 * The configuration a newly inserted step starts with: the least the schema
 * accepts for its type, until the configuration panels (#12, #13) let a
 * developer change it.
 */
export const newStepConfiguration = {
  filter: { expression: 'true' },
  transform: { mappings: [{ targetField: 'value', expression: 'value' }] },
  validate: { rules: [{ field: 'value' }], onInvalid: 'drop' },
  customCode: { entrypoint: 'process' },
} as const satisfies {
  filter: FilterStep['configuration'];
  transform: TransformStep['configuration'];
  validate: ValidateStep['configuration'];
  customCode: CustomCodeStep['configuration'];
};

/** A new step of this type with this id, configured as above. */
export function newStep(type: StepType, id: string): ProcessingStep {
  // A deep copy, so no two steps share a configuration object.
  const configuration = structuredClone(newStepConfiguration[type]);
  switch (type) {
    case 'filter':
      return { id, type, configuration } as FilterStep;
    case 'transform':
      return { id, type, configuration } as TransformStep;
    case 'validate':
      return { id, type, configuration } as ValidateStep;
    case 'customCode':
      return { id, type, configuration } as CustomCodeStep;
  }
}

/**
 * The id a step of this type inserted into this pipeline gets: the type and
 * the smallest number that makes it unlike every id already in the pipeline,
 * the source's and destination's included. It is chosen once, at insertion,
 * and no later operation changes it, so it stays the step's id.
 */
export function newStepId(pipeline: Pipeline, type: StepType): string {
  const taken = new Set([
    pipeline.source.id,
    pipeline.destination.id,
    ...steps(pipeline).map((step) => step.id),
  ]);
  for (let n = 1; ; n++) {
    const id = `${type}-${n}`;
    if (!taken.has(id)) return id;
  }
}

const steps = (pipeline: Pipeline): readonly ProcessingStep[] =>
  pipeline.processingSteps ?? [];

/**
 * A block's place in the chain: 0 is the source, then each step in order,
 * and the destination last. `null` for a step the pipeline does not have.
 */
export function chainIndexOf(pipeline: Pipeline, ref: BlockRef): number | null {
  switch (ref.role) {
    case 'source':
      return 0;
    case 'destination':
      return steps(pipeline).length + 1;
    case 'step': {
      const index = steps(pipeline).findIndex((step) => step.id === ref.id);
      return index === -1 ? null : index + 1;
    }
  }
}

const describe = (ref: BlockRef): string =>
  ref.role === 'step' ? `step "${ref.id}"` : `the ${ref.role}`;

const reject = (pipeline: Pipeline, reason: string): EditResult => ({
  ok: false,
  pipeline,
  reason,
});

/**
 * The new pipeline, if the schema accepts it; otherwise a rejection that
 * hands back the previous one. Every operation ends here.
 */
export function commit(
  previous: Pipeline,
  next: Pipeline,
  stepId?: string,
): EditResult {
  const parsed = pipelineZodSchema.safeParse(next);
  if (!parsed.success) {
    const first = parsed.error.issues[0];
    const where = first === undefined ? '' : first.path.join('.');
    return reject(
      previous,
      `The result would not be a valid pipeline${
        where === '' ? '' : ` (${where})`
      }: ${first?.message ?? 'rejected by the schema'}`,
    );
  }
  // The model as built, not the parse output: parsing fills in defaults the
  // author never wrote, and an edit should not add them.
  return stepId === undefined
    ? { ok: true, pipeline: next }
    : { ok: true, pipeline: next, stepId };
}

/** The pipeline with these steps, as a new object. */
const withSteps = (
  pipeline: Pipeline,
  processingSteps: ProcessingStep[],
): Pipeline => ({ ...pipeline, processingSteps });

/**
 * Inserts a new step of this type between two blocks that are next to each
 * other in the chain, `before` coming first. The step gets an id from
 * newStepId.
 */
export function insertStep(
  pipeline: Pipeline,
  between: { before: BlockRef; after: BlockRef },
  type: StepType,
): EditResult {
  const before = chainIndexOf(pipeline, between.before);
  const after = chainIndexOf(pipeline, between.after);
  if (before === null) {
    return reject(pipeline, `There is no ${describe(between.before)}.`);
  }
  if (after === null) {
    return reject(pipeline, `There is no ${describe(between.after)}.`);
  }
  if (after !== before + 1) {
    return reject(
      pipeline,
      `${describe(between.before)} and ${describe(between.after)} are not next to each other, so nothing can go between them.`,
    );
  }
  const id = newStepId(pipeline, type);
  const next = [...steps(pipeline)];
  // Chain index `after` is step index `after - 1`: the new step takes the
  // place of the block after the gap, and that block moves one along.
  next.splice(after - 1, 0, newStep(type, id));
  return commit(pipeline, withSteps(pipeline, next), id);
}

/**
 * Moves a processing step to another place in the chain, counted the way
 * chainIndexOf counts once the move is done. The source stays first and the
 * destination last: neither can be moved, and a step cannot be moved to the
 * source's place or before it, or to the destination's place or after it.
 */
export function moveStep(
  pipeline: Pipeline,
  ref: BlockRef,
  toChainIndex: number,
): EditResult {
  if (ref.role !== 'step') {
    return reject(
      pipeline,
      `The ${ref.role} cannot be moved: it is always ${
        ref.role === 'source' ? 'first' : 'last'
      }.`,
    );
  }
  const from = chainIndexOf(pipeline, ref);
  if (from === null) return reject(pipeline, `There is no ${describe(ref)}.`);
  const count = steps(pipeline).length;
  if (!Number.isInteger(toChainIndex)) {
    return reject(pipeline, `${toChainIndex} is not a place in the chain.`);
  }
  if (toChainIndex < 1) {
    return reject(
      pipeline,
      'A processing step cannot go before the source: the source is always first.',
    );
  }
  if (toChainIndex > count) {
    return reject(
      pipeline,
      'A processing step cannot go after the destination: the destination is always last.',
    );
  }
  const next = [...steps(pipeline)];
  const [moved] = next.splice(from - 1, 1);
  next.splice(toChainIndex - 1, 0, moved as ProcessingStep);
  return commit(pipeline, withSteps(pipeline, next));
}

/**
 * Deletes a processing step. The source and destination cannot be deleted;
 * deleting the last remaining step leaves the source connected straight to
 * the destination.
 */
export function deleteStep(pipeline: Pipeline, ref: BlockRef): EditResult {
  if (ref.role !== 'step') {
    return reject(
      pipeline,
      `The ${ref.role} cannot be deleted: every pipeline has one.`,
    );
  }
  const index = chainIndexOf(pipeline, ref);
  if (index === null) return reject(pipeline, `There is no ${describe(ref)}.`);
  const next = steps(pipeline).filter((_, i) => i !== index - 1);
  return commit(pipeline, withSteps(pipeline, next));
}
