// The graph's measurements, and the arithmetic over them that does not need
// the graph library: every node is the same size and the same gap apart, in
// one row, so a node's place in the chain and its x coordinate are two names
// for the same thing. Kept apart from layout.ts so that the editing transforms
// (edit.ts) and the drag decision (drag.ts) can use it without importing
// React or React Flow.

/** The size of every node, in graph units. */
export const nodeWidth = 180;
export const nodeHeight = 56;
/** The horizontal space between one node and the next. */
export const nodeGap = 60;
/** From one node's left edge to the next one's. */
export const nodePitch = nodeWidth + nodeGap;

/** The x of the node at this place in the chain (0 is the source). */
export const xAtChainIndex = (index: number): number => index * nodePitch;

/**
 * The place in the chain a node dropped with its left edge at `x` lands in:
 * the slot whose node it overlaps most. It can be below 0 or past the end of
 * the chain; what to make of that is the caller's to decide.
 */
export const chainIndexAt = (x: number): number => Math.round(x / nodePitch);
