// The pipeline graph: a validated Pipeline drawn as a chain of nodes that can
// be panned, zoomed, fitted to the view and selected in, and edited (#11): a
// processing step can be dragged to another place in the chain, or deleted
// with the delete key. Inserting a step, and the same moves and deletes
// without a pointer, are in the side panel (EditControls.tsx).
//
// It renders from the model and nothing else. The nodes and edges are
// computed from the pipeline on every change of it (layout.ts) and are never
// kept as state of their own, so nothing the graph library does can move,
// add or remove a node by itself. An edit is a pure transform of the model
// (edit.ts) whose result is handed out through onEdit; the graph changes only
// when whoever owns the pipeline (the shell) hands a new one back in. The one
// thing kept here is where a step being dragged is right now, which is
// dropped the moment the drag ends. Which node is selected is handed in and
// reported out, like the pipeline.
//
// Each node is drawn by the block's own component (#10). A custom-code node
// also shows its step's file (#14): the shell hands in `code`, the buffers it
// owns, and each node reads only its own path's buffer from it, so typing in
// one editor redraws only the nodes showing that file. The editor hangs
// below the card, outside the fixed node box the layout and the drop slots
// use, and nothing in it reaches the graph: no key, wheel or drag.

import '@xyflow/react/dist/style.css';
import './graph.css';

import type {
  NodeChange,
  NodeProps,
  NodeTypes,
  OnNodeDrag,
} from '@xyflow/react';
import {
  Background,
  Controls,
  Handle,
  Position,
  ReactFlow,
} from '@xyflow/react';
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from 'react';

import type { CustomCodeStep, Language, Pipeline } from '@kpv/schema';

import type { CodeActions } from '../blocks/BlockNode.tsx';
import { BlockNodeView } from '../blocks/BlockNode.tsx';
import type { SourceBuffer } from '../blocks/source-buffer.ts';
import { codeView, stepSource } from '../blocks/source-buffer.ts';

import type { DragEnd } from './drag.ts';
import { dragResult } from './drag.ts';
import type { EditResult } from './edit.ts';
import { deleteStep } from './edit.ts';
import type { BlockNode } from './layout.ts';
import { blockForNode, blockRef, layoutPipeline } from './layout.ts';
import { nodeHeight, nodeWidth } from './geometry.ts';

/**
 * The source files the custom-code nodes show, from whoever owns them (the
 * shell). Without it every custom-code node says there is no file to read.
 */
export interface CodeSources extends CodeActions {
  subscribe: (listener: () => void) => () => void;
  /** The buffer of one path, the same object until it changes. */
  buffer: (path: string) => SourceBuffer | undefined;
  /** The pipeline's language, which picks the highlighting. */
  language: Language;
  /** The connection state's words while a save cannot be sent, else null. */
  offline: string | null;
}

const CodeContext = createContext<CodeSources | null>(null);

const noSubscription = () => () => undefined;

/** A custom-code block's node, showing its own path's buffer. */
function CustomCodeBlock(props: { step: CustomCodeStep }) {
  const { step } = props;
  const sources = useContext(CodeContext);
  const source = stepSource(step) ?? { kind: 'none' as const };
  const path = source.kind === 'file' ? source.path : null;
  const buffer = useSyncExternalStore(
    sources?.subscribe ?? noSubscription,
    () => (sources === null || path === null ? null : sources.buffer(path)),
    () => (sources === null || path === null ? null : sources.buffer(path)),
  );
  const view = codeView(
    source,
    sources === null ? null : buffer,
    sources?.language ?? 'python',
    sources?.offline ?? null,
  );
  return sources === null ? (
    <BlockNodeView block={step} code={view} />
  ) : (
    <BlockNodeView block={step} code={view} actions={sources} />
  );
}

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
      {data.block.type === 'customCode' ? (
        <CustomCodeBlock step={data.block} />
      ) : (
        <BlockNodeView block={data.block} />
      )}
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
 * The switches that make the view an editor of the chain and nothing more:
 * a processing step can be dragged and deleted (each node says whether it
 * can, layout.ts), but nothing can be connected or reconnected, since the
 * chain's edges follow from its order. Selection is one node at a time by
 * click or keyboard. Panning, zooming and fitting stay on.
 */
export const editProps = {
  nodesDraggable: true,
  nodesConnectable: false,
  edgesReconnectable: false,
  edgesFocusable: false,
  elementsSelectable: true,
  nodesFocusable: true,
  deleteKeyCode: ['Delete', 'Backspace'] as string[],
  selectionKeyCode: null,
  multiSelectionKeyCode: null,
  selectionOnDrag: false,
  selectNodesOnDrag: false,
  connectOnClick: false,
  // The library's own screen reader text offers moving nodes with the arrow
  // keys, which this view does not do; the side panel moves a step instead.
  ariaLabelConfig: {
    'node.a11yDescription.default':
      'Press enter or space to select a block, and escape to clear the selection. Delete removes a selected processing step; the side panel moves one or inserts another.',
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
 * measured size, a drag, a removal) that are handled elsewhere or not at all.
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

/** Where a drag ended, from the pointer event that ended it. */
function dragEnd(
  event: MouseEvent | TouchEvent,
  canvas: HTMLElement | null,
  x: number,
): DragEnd {
  const point = 'changedTouches' in event ? event.changedTouches[0] : event;
  if (canvas === null || point === undefined) return { kind: 'outside' };
  const box = canvas.getBoundingClientRect();
  const inside =
    point.clientX >= box.left &&
    point.clientX <= box.right &&
    point.clientY >= box.top &&
    point.clientY <= box.bottom;
  return inside ? { kind: 'dropped', x } : { kind: 'outside' };
}

export function PipelineGraph(props: {
  pipeline: Pipeline;
  selected: string | null;
  onSelect: (id: string | null) => void;
  /** Every edit made on the graph, accepted or rejected. */
  onEdit: (result: EditResult) => void;
  /** The custom-code nodes' files, for a pipeline opened from disk. */
  code?: CodeSources;
}) {
  const { pipeline, selected, onSelect, onEdit, code = null } = props;
  const layout = useMemo(() => layoutPipeline(pipeline), [pipeline]);
  // The step being dragged and its x right now. `cancelled` holds it back
  // after escape until the pointer is let go, so the rest of that drag
  // moves nothing.
  const [drag, setDrag] = useState<{ id: string; x: number } | null>(null);
  const cancelled = useRef(false);
  const canvas = useRef<HTMLDivElement>(null);
  const dragging = drag !== null;
  useEffect(() => {
    if (!dragging) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return;
      // Escape typed in a code editor is the editor's.
      if (
        event.target instanceof Element &&
        event.target.closest('.code-editor') !== null
      ) {
        return;
      }
      cancelled.current = true;
      setDrag(null);
    };
    window.addEventListener('keydown', onKey, true);
    return () => window.removeEventListener('keydown', onKey, true);
  }, [dragging]);
  const nodes = useMemo(
    () =>
      layout.nodes.map((node) => ({
        ...node,
        selected: node.id === selected,
        // Every node has the fixed size the layout gives it, so it is
        // measured before the library measures it; dragging needs that, and
        // the size changes the library reports are not kept (see below).
        measured: { width: nodeWidth, height: nodeHeight },
        // A dragged step follows the pointer along the row, never off it.
        position:
          drag !== null && drag.id === node.id
            ? { x: drag.x, y: 0 }
            : node.position,
      })),
    [layout, selected, drag],
  );
  const onNodesChange = useCallback(
    (changes: NodeChange<BlockNode>[]) => {
      for (const change of changes) {
        // Only a pointer drag moves a node; the arrow keys do not.
        if (
          change.type === 'position' &&
          change.dragging === true &&
          change.position !== undefined &&
          !cancelled.current
        ) {
          setDrag({ id: change.id, x: change.position.x });
        }
        if (change.type === 'remove') {
          const placed = blockForNode(pipeline, change.id);
          if (placed !== null) onEdit(deleteStep(pipeline, blockRef(placed)));
        }
      }
      const next = selectionAfter(selected, changes);
      if (next !== selected) onSelect(next);
    },
    [pipeline, selected, onSelect, onEdit],
  );
  const onNodeDragStart = useCallback<OnNodeDrag<BlockNode>>(() => {
    cancelled.current = false;
  }, []);
  const onNodeDragStop = useCallback<OnNodeDrag<BlockNode>>(
    (event, node) => {
      const end: DragEnd = cancelled.current
        ? { kind: 'cancelled' }
        : dragEnd(event, canvas.current, node.position.x);
      cancelled.current = false;
      setDrag(null);
      const placed = blockForNode(pipeline, node.id);
      if (placed === null) return;
      const result = dragResult(pipeline, blockRef(placed), end);
      if (result !== null) onEdit(result);
    },
    [pipeline, onEdit],
  );
  return (
    <CodeContext.Provider value={code}>
      <div className="pipeline-graph" data-pipeline={pipeline.id} ref={canvas}>
        <ReactFlow<BlockNode>
          nodes={nodes}
          edges={layout.edges}
          nodeTypes={nodeTypes}
          onNodesChange={onNodesChange}
          onNodeDragStart={onNodeDragStart}
          onNodeDragStop={onNodeDragStop}
          fitView
          minZoom={minZoom}
          {...editProps}
        >
          <Background />
          <Controls showInteractive={false} />
        </ReactFlow>
      </div>
    </CodeContext.Provider>
  );
}
