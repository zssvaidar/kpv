import { describe, expect, it } from 'vitest';

import type { Pipeline } from '@kpv/schema';

import shared from '../../../../schema/fixtures/pipelines.json';
import { graphFixtures } from './fixtures.ts';
import type { BlockNode } from './layout.ts';
import {
  blockForNode,
  layoutPipeline,
  nodeGap,
  nodeHeight,
  nodeWidth,
} from './layout.ts';
import long from './long-pipeline.json';
import { minZoom } from './PipelineGraph.tsx';

const ids = (nodes: BlockNode[]) => nodes.map((node) => node.id);
const chain = (pipeline: Pipeline) =>
  layoutPipeline(pipeline).edges.map((edge) => [edge.source, edge.target]);

/** Every key anywhere in a JSON value. */
const keys = (value: unknown): string[] => {
  if (Array.isArray(value)) return value.flatMap(keys);
  if (typeof value === 'object' && value !== null) {
    return Object.entries(value).flatMap(([key, inner]) => [
      key,
      ...keys(inner),
    ]);
  }
  return [];
};

describe('layoutPipeline', () => {
  it('lays the every-block-type fixture out as an ordered chain, source leftmost and destination rightmost', () => {
    const pipeline = graphFixtures['every-block-type'];
    const { nodes } = layoutPipeline(pipeline);
    const steps = pipeline.processingSteps ?? [];

    expect(ids(nodes)).toEqual([
      'source',
      ...steps.map((step) => `step:${step.id}`),
      'destination',
    ]);
    expect(nodes.map((node) => node.data.block)).toEqual([
      pipeline.source,
      ...steps,
      pipeline.destination,
    ]);
    const xs = nodes.map((node) => node.position.x);
    expect(xs).toEqual([...xs].sort((a, b) => a - b));
    expect(new Set(xs).size).toBe(xs.length);
    expect(nodes.every((node) => node.position.y === 0)).toBe(true);

    const order = ids(nodes);
    expect(chain(pipeline)).toEqual(
      order.slice(1).map((target, i) => [order[i], target]),
    );
  });

  it('computes every position itself: no fixture supplies one', () => {
    const fixtureKeys = new Set([
      ...keys(shared.valid.map((fixture) => fixture.pipeline)),
      ...keys(long),
    ]);
    for (const coordinate of ['position', 'x', 'y', 'width', 'height']) {
      expect(fixtureKeys.has(coordinate)).toBe(false);
    }
    // And the layout reads nothing but the model: two pipelines with the
    // same number of blocks are placed identically.
    const positions = (pipeline: Pipeline) =>
      layoutPipeline(pipeline).nodes.map((node) => node.position);
    const renamed: Pipeline = {
      ...graphFixtures['zero-processing-steps'],
      id: 'other',
      source: { id: 'in', type: 'kafkaSource', topic: 'in.v1' },
    };
    expect(positions(renamed)).toEqual(
      positions(graphFixtures['zero-processing-steps']),
    );
  });

  it('connects the source straight to the destination when there are zero steps', () => {
    expect(chain(graphFixtures['zero-processing-steps'])).toEqual([
      ['source', 'destination'],
    ]);
    const absent = shared.valid.find(
      (fixture) => fixture.name === 'source-and-destination-only',
    )?.pipeline as Pipeline;
    expect(absent.processingSteps).toBeUndefined();
    expect(chain(absent)).toEqual([['source', 'destination']]);
  });

  it('lays a long pipeline out without overlapping nodes, inside what zoom can fit', () => {
    const { nodes } = layoutPipeline(graphFixtures.long);
    expect(nodes.length).toBe(32);
    for (const node of nodes) {
      expect(node.width).toBe(nodeWidth);
      expect(node.height).toBe(nodeHeight);
    }
    for (let i = 1; i < nodes.length; i++) {
      const before = nodes[i - 1] as BlockNode;
      const after = nodes[i] as BlockNode;
      expect(after.position.x).toBeGreaterThanOrEqual(
        before.position.x + nodeWidth + nodeGap,
      );
    }
    // Fit-to-view can only bring all of it into frame if zooming out that
    // far is allowed: at the minimum zoom the whole chain is narrower than
    // a 640px-wide graph area.
    const last = nodes[nodes.length - 1] as BlockNode;
    expect((last.position.x + nodeWidth) * minZoom).toBeLessThan(640);
  });

  it('gives the same layout every time for the same model', () => {
    for (const pipeline of Object.values(graphFixtures)) {
      const first = layoutPipeline(pipeline);
      expect(layoutPipeline(pipeline)).toEqual(first);
      // A fresh copy of the same model, as a re-fetch would give.
      expect(
        layoutPipeline(JSON.parse(JSON.stringify(pipeline)) as Pipeline),
      ).toEqual(first);
    }
  });

  it('keeps node ids distinct when a step shares an id with the source', () => {
    const pipeline: Pipeline = {
      ...graphFixtures['zero-processing-steps'],
      processingSteps: [
        {
          id: 'orders',
          type: 'filter',
          configuration: { expression: 'true' },
        },
      ],
    };
    const nodeIds = ids(layoutPipeline(pipeline).nodes);
    expect(new Set(nodeIds).size).toBe(nodeIds.length);
  });
});

describe('blockForNode', () => {
  it('finds the block a node id stands for', () => {
    const pipeline = graphFixtures['every-block-type'];
    expect(blockForNode(pipeline, 'source')?.block).toBe(pipeline.source);
    expect(blockForNode(pipeline, 'destination')?.block).toBe(
      pipeline.destination,
    );
    expect(blockForNode(pipeline, 'step:only-paid')?.block.type).toBe('filter');
    expect(blockForNode(pipeline, 'step:missing')).toBeNull();
    expect(blockForNode(pipeline, null)).toBeNull();
  });
});
