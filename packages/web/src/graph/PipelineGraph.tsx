// The pipeline graph: a validated Pipeline drawn as a read-only chain of
// nodes that can be panned, zoomed, fitted to the view and selected in.
//
// It renders from the model and nothing else. The nodes and edges are
// computed from the pipeline on every change of it (layout.ts) and are never
// kept as state of their own, so nothing the graph library does can move,
// add or remove a node. The one thing the view changes is which node is
// selected, and that is not kept here either: it is handed in and reported
// out, so whoever owns it (the shell) can show it in the side panel.
//
// Editing is #11. Each node is drawn by the block's own component (#10).

import '@xyflow/react/dist/style.css';
import './graph.css';

import type { NodeChange, NodeProps, NodeTypes } from '@xyflow/react';
import {
  Background,
  Controls,
  Handle,
  Position,
  ReactFlow,
} from '@xyflow/react';
import { useCallback, useMemo } from 'react';

import type { Pipeline } from '@kpv/schema';

import { BlockNodeView } from '../blocks/BlockNode.tsx';

import type { BlockNode } from './layout.ts';
import { layoutPipeline } from './layout.ts';

/**
 * A block's node on the graph: its block component (#10) between the handles
 * its edges attach to, which are neither connectable nor visible. A source
 * has nothing coming in and a destination nothing going out.
 */
function GraphBlockNode(props: NodeProps<BlockNode>) {
  const { data } = props;
  return (
    <>
      {data.role === 'source' ? null : (
        <Handle
          type="target"
          position={Position.Left}
          isConnectable={false}
          isConnectableStart={false}
          isConnectableEnd={false}
          className="block-handle"
        />
      )}
      <BlockNodeView block={data.block} />
      {data.role === 'destination' ? null : (
        <Handle
          type="source"
          position={Position.Right}
          isConnectable={false}
          isConnectableStart={false}
          isConnectableEnd={false}
          className="block-handle"
        />
      )}
    </>
  );
}

const nodeTypes: NodeTypes = { block: GraphBlockNode };

/**
 * The switches that make the view read-only: nothing can be dragged,
 * connected, reconnected or deleted, and selection is one node at a time by
 * click or keyboard. Panning, zooming and fitting stay on.
 */
export const readOnlyProps = {
  nodesDraggable: false,
  nodesConnectable: false,
  edgesReconnectable: false,
  edgesFocusable: false,
  elementsSelectable: true,
  nodesFocusable: true,
  deleteKeyCode: null,
  selectionKeyCode: null,
  multiSelectionKeyCode: null,
  selectionOnDrag: false,
  selectNodesOnDrag: false,
  connectOnClick: false,
  // The library's own screen reader text offers moving and deleting nodes,
  // which this view does not do.
  ariaLabelConfig: {
    'node.a11yDescription.default':
      'Press enter or space to select a block, and escape to clear the selection.',
    'node.a11yDescription.keyboardDisabled':
      'Press enter or space to select a block, and escape to clear the selection.',
    'edge.a11yDescription.default': 'A connection between two blocks.',
  },
} as const;

/** How far out zoom goes, so a long pipeline still fits the view. */
export const minZoom = 0.05;

/**
 * Which node the view selects after these changes, given the one selected
 * before. Only selection changes count; the graph reports others (a node's
 * measured size, for one) that a read-only view has nothing to do with.
 */
export function selectionAfter(
  current: string | null,
  changes: NodeChange[],
): string | null {
  let next = current;
  for (const change of changes) {
    if (change.type !== 'select') continue;
    if (change.selected) {
      next = change.id;
    } else if (change.id === next) {
      next = null;
    }
  }
  return next;
}

export function PipelineGraph(props: {
  pipeline: Pipeline;
  selected: string | null;
  onSelect: (id: string | null) => void;
}) {
  const { pipeline, selected, onSelect } = props;
  const layout = useMemo(() => layoutPipeline(pipeline), [pipeline]);
  const nodes = useMemo(
    () =>
      layout.nodes.map((node) => ({ ...node, selected: node.id === selected })),
    [layout, selected],
  );
  const onNodesChange = useCallback(
    (changes: NodeChange<BlockNode>[]) => {
      const next = selectionAfter(selected, changes);
      if (next !== selected) onSelect(next);
    },
    [selected, onSelect],
  );
  return (
    <div className="pipeline-graph" data-pipeline={pipeline.id}>
      <ReactFlow<BlockNode>
        nodes={nodes}
        edges={layout.edges}
        nodeTypes={nodeTypes}
        onNodesChange={onNodesChange}
        fitView
        minZoom={minZoom}
        {...readOnlyProps}
      >
        <Background />
        <Controls showInteractive={false} />
      </ReactFlow>
    </div>
  );
}
