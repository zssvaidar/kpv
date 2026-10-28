// What the side panel shows of the graph's selection. The configuration
// panels (#12, #13) key off the same selected node id and blockForNode.

import type { Pipeline } from '@kpv/schema';

import { blockForNode } from './layout.ts';
import { blockTypeLabel } from './PipelineGraph.tsx';

export function SelectionSummary(props: {
  pipeline: Pipeline;
  selected: string | null;
}) {
  const placed = blockForNode(props.pipeline, props.selected);
  if (placed === null) {
    return (
      <p className="muted" data-selected="">
        No block selected.
      </p>
    );
  }
  return (
    <p className="selection" data-selected={props.selected ?? ''}>
      Selected: {blockTypeLabel[placed.block.type]}{' '}
      <code>{placed.block.id}</code>
    </p>
  );
}
