// The create form's draft and its checks (#48), apart from React. Every field
// is checked as it is typed with schemaIssues (graph/edit.ts), the generated
// pipeline Zod: the draft is put into the pipeline the agent would write, and
// each issue is shown at the field it points into. Nothing here restates
// what a valid name or topic is.

import type { CreatePipelineRequest, Pipeline } from '@kpv/schema';

import { schemaIssues } from '../graph/edit.ts';

/** The fields a person fills in. The root is picked, never typed. */
export type CreateField =
  'name' | 'language' | 'sourceTopic' | 'destinationTopic' | 'networkMode';

export const createFields: readonly CreateField[] = [
  'name',
  'language',
  'sourceTopic',
  'destinationTopic',
  'networkMode',
];

/**
 * What the form holds. The schema gives the topics and the network mode no
 * default, and the language has none either, so a new draft has none of them
 * chosen: '' is "not chosen yet". The root is '' until one is picked.
 */
export type CreateDraft = Record<CreateField, string> & { root: string };

export function emptyDraft(root = ''): CreateDraft {
  return {
    root,
    name: '',
    language: '',
    sourceTopic: '',
    destinationTopic: '',
    networkMode: '',
  };
}

/** Where in the pipeline each field lands. */
const fieldPaths: Record<CreateField, readonly string[]> = {
  name: ['name'],
  language: ['language'],
  sourceTopic: ['source', 'topic'],
  destinationTopic: ['destination', 'topic'],
  networkMode: ['runtimeConfiguration', 'networkMode'],
};

/**
 * The pipeline a draft describes, as the agent builds it: the id is the
 * agent's to derive from the name, so a placeholder that is always valid
 * stands in for it here.
 */
export function draftPipeline(draft: CreateDraft): Pipeline {
  return {
    id: 'pipeline',
    name: draft.name,
    language: draft.language,
    source: { id: 'source', type: 'kafkaSource', topic: draft.sourceTopic },
    destination: {
      id: 'destination',
      type: 'kafkaDestination',
      topic: draft.destinationTopic,
    },
    runtimeConfiguration: { networkMode: draft.networkMode },
  } as unknown as Pipeline;
}

/** Each field's problem, or null when the schema accepts it. */
export function fieldProblems(
  draft: CreateDraft,
): Record<CreateField, string | null> {
  const issues = schemaIssues(draftPipeline(draft));
  const out = Object.fromEntries(
    createFields.map((field) => [field, null]),
  ) as Record<CreateField, string | null>;
  for (const field of createFields) {
    const path = fieldPaths[field];
    const issue = issues.find(
      (candidate) =>
        candidate.path.length >= path.length &&
        path.every((part, index) => candidate.path[index] === part),
    );
    if (issue !== undefined) {
      out[field] =
        draft[field] === ''
          ? field === 'language' || field === 'networkMode'
            ? 'Choose one.'
            : 'Fill this in.'
          : issue.message;
    }
  }
  return out;
}

/**
 * The request a draft sends, or null while any field is invalid or no root
 * is picked: the form cannot be submitted then.
 */
export function createRequest(
  draft: CreateDraft,
  roots: readonly string[],
): CreatePipelineRequest | null {
  if (!roots.includes(draft.root)) return null;
  const problems = fieldProblems(draft);
  if (createFields.some((field) => problems[field] !== null)) return null;
  return {
    root: draft.root,
    name: draft.name,
    language: draft.language,
    sourceTopic: draft.sourceTopic,
    destinationTopic: draft.destinationTopic,
    networkMode: draft.networkMode,
  };
}
