// The side panel's editing controls for the selected block (#11): insert a
// processing step right after it, and for a processing step, move it one
// place earlier or later or delete it. They do what dragging and the delete
// key do on the graph, for someone not using a pointer, and inserting is only
// here. Each button calls a transform from edit.ts and hands its result out;
// nothing here changes the pipeline itself.

import type { Pipeline } from '@kpv/schema';

import { blockKindLabel } from '../blocks/BlockNode.tsx';
import type { EditResult } from './edit.ts';
import { deleteStep, insertStep, moveStep, stepTypes } from './edit.ts';
import { blockForNode, blockRef, placedBlocks } from './layout.ts';

export function EditControls(props: {
  pipeline: Pipeline;
  selected: string | null;
  onEdit: (result: EditResult) => void;
}) {
  const { pipeline, onEdit } = props;
  const placed = blockForNode(pipeline, props.selected);
  if (placed === null) return null;
  const ref = blockRef(placed);
  const next = placedBlocks(pipeline)[placed.index + 1];
  const stepCount = pipeline.processingSteps?.length ?? 0;
  return (
    <div className="edit-controls" aria-label="Edit the pipeline">
      {next === undefined ? null : (
        <fieldset>
          <legend>Insert a step after this block</legend>
          {stepTypes.map((type) => (
            <button
              key={type}
              type="button"
              data-insert={type}
              onClick={() =>
                onEdit(
                  insertStep(
                    pipeline,
                    { before: ref, after: blockRef(next) },
                    type,
                  ),
                )
              }
            >
              {blockKindLabel[type]}
            </button>
          ))}
        </fieldset>
      )}
      {placed.role !== 'step' ? null : (
        <fieldset>
          <legend>This step</legend>
          <button
            type="button"
            data-action="move-earlier"
            disabled={placed.index <= 1}
            onClick={() => onEdit(moveStep(pipeline, ref, placed.index - 1))}
          >
            Move earlier
          </button>
          <button
            type="button"
            data-action="move-later"
            disabled={placed.index >= stepCount}
            onClick={() => onEdit(moveStep(pipeline, ref, placed.index + 1))}
          >
            Move later
          </button>
          <button
            type="button"
            data-action="delete"
            onClick={() => onEdit(deleteStep(pipeline, ref))}
          >
            Delete step
          </button>
        </fieldset>
      )}
    </div>
  );
}
