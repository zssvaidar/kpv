// Where each block of a pipeline goes on the graph, computed from the model
// alone. A pipeline is linear (one source, ordered steps, one destination), so
// its layout is a single left-to-right row in pipeline order: source leftmost,
// destination rightmost, every node the same size and the same gap apart.
//
// Nothing here reads or keeps coordinates from anywhere else: no fixture and
// no author supplies a position, and the same pipeline always gives the same
// nodes and edges, so the graph does not move between renders. Every node has
// a fixed size, which is what keeps a long pipeline from overlapping itself
// however long its labels are.

import type { Edge, Node } from '@xyflow/react';
import { Position } from '@xyflow/react';

import type {
  KafkaDestinationBlock,
  KafkaSourceBlock,
  Pipeline,
  ProcessingStep,
} from '@kpv/schema';

/** The size of every node, in graph units. */
export const nodeWidth = 180;
export const nodeHeight = 56;
/** The horizontal space between one node and the next. */
export const nodeGap = 60;

/** Where a block sits in the pipeline. */
export type BlockRole = 'source' | 'step' | 'destination';

/** One block of a pipeline and where it sits, which is what a node shows. */
export type PlacedBlock =
  | { role: 'source'; index: 0; block: KafkaSourceBlock }
  | { role: 'step'; index: number; block: ProcessingStep }
  | { role: 'destination'; index: number; block: KafkaDestinationBlock };

/**
 * A node's data: the block it stands for, and its place in the chain.
 * Block-specific node components are #10; this is what they will receive.
 */
export type BlockNodeData = PlacedBlock & Record<string, unknown>;

export type BlockNode = Node<BlockNodeData, 'block'>;

/**
 * The node id of a block. Source and destination ids are only unique among
 * their siblings, so a step may share one with them; prefixing by role keeps
 * every node id in a pipeline distinct.
 */
export const nodeId = (placed: PlacedBlock): string =>
  placed.role === 'step' ? `step:${placed.block.id}` : placed.role;

/** Every block of a pipeline, in pipeline order. */
export function placedBlocks(pipeline: Pipeline): PlacedBlock[] {
  const steps = pipeline.processingSteps ?? [];
  return [
    { role: 'source', index: 0, block: pipeline.source },
    ...steps.map((block, i): PlacedBlock => ({
      role: 'step',
      index: i + 1,
      block,
    })),
    {
      role: 'destination',
      index: steps.length + 1,
      block: pipeline.destination,
    },
  ];
}

/** The block a node id stands for, or `null` when the pipeline has none. */
export function blockForNode(
  pipeline: Pipeline,
  id: string | null,
): PlacedBlock | null {
  if (id === null) return null;
  return placedBlocks(pipeline).find((placed) => nodeId(placed) === id) ?? null;
}

/** The nodes and edges of a pipeline, laid out left to right. */
export function layoutPipeline(pipeline: Pipeline): {
  nodes: BlockNode[];
  edges: Edge[];
} {
  const blocks = placedBlocks(pipeline);
  const nodes = blocks.map((placed): BlockNode => ({
    id: nodeId(placed),
    type: 'block',
    position: { x: placed.index * (nodeWidth + nodeGap), y: 0 },
    width: nodeWidth,
    height: nodeHeight,
    sourcePosition: Position.Right,
    targetPosition: Position.Left,
    data: { ...placed },
  }));
  const edges = nodes.slice(1).map((target, i): Edge => {
    // slice(1) is one shorter than nodes, so nodes[i] is always there.
    const source = nodes[i] as BlockNode;
    return {
      id: `${source.id}->${target.id}`,
      source: source.id,
      target: target.id,
    };
  });
  return { nodes, edges };
}
