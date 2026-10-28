import type { NodeChange } from '@xyflow/react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';

import { Shell } from '../shell/App.tsx';
import { graphFixtures } from './fixtures.ts';
import {
  PipelineGraph,
  readOnlyProps,
  selectionAfter,
} from './PipelineGraph.tsx';
import { SelectionSummary } from './SelectionSummary.tsx';

const select = (id: string, selected: boolean): NodeChange => ({
  type: 'select',
  id,
  selected,
});

const render = (selected: string | null) =>
  renderToStaticMarkup(
    <PipelineGraph
      pipeline={graphFixtures['every-block-type']}
      selected={selected}
      onSelect={() => undefined}
    />,
  );

describe('PipelineGraph', () => {
  it('draws one node per block, in pipeline order', () => {
    const html = render(null);
    const drawn = [
      ...html.matchAll(/data-id="([^"]+)" data-testid="rf__node/g),
    ].map((match) => match[1]);
    expect(drawn).toEqual([
      'source',
      'step:only-paid',
      'step:to-billing-shape',
      'step:require-amount',
      'step:apply-discounts',
      'destination',
    ]);
  });

  it('marks the selected node, and only that one', () => {
    const html = render('step:require-amount');
    const selected = [
      ...html.matchAll(
        /class="react-flow__node[^"]*\bselected\b[^"]*"[^>]*data-id="([^"]+)"/g,
      ),
    ].map((match) => match[1]);
    expect(selected).toEqual(['step:require-amount']);
  });

  it('is read-only: nothing drags, connects, reconnects or deletes', () => {
    expect(readOnlyProps).toMatchObject({
      nodesDraggable: false,
      nodesConnectable: false,
      edgesReconnectable: false,
      deleteKeyCode: null,
      connectOnClick: false,
    });
    const html = render(null);
    expect(html).not.toMatch(/react-flow__node[^"]*\bdraggable\b/);
    expect(html).not.toMatch(/react-flow__handle[^"]*\bconnectable/);
    // Fit, zoom in and zoom out; no interactivity toggle that would unlock
    // dragging.
    expect(html).toContain('react-flow__controls-fitview');
    expect(html).toContain('react-flow__controls-zoomin');
    expect(html).toContain('react-flow__controls-zoomout');
    expect(html).not.toContain('react-flow__controls-interactive');
  });
});

describe('selectionAfter', () => {
  it('follows a click on a node', () => {
    expect(selectionAfter(null, [select('source', true)])).toBe('source');
    expect(
      selectionAfter('source', [
        select('source', false),
        select('destination', true),
      ]),
    ).toBe('destination');
  });

  it('clears when the selected node is deselected, as a click on the pane does', () => {
    expect(selectionAfter('source', [select('source', false)])).toBeNull();
  });

  it('ignores every change that is not about selection', () => {
    const changes: NodeChange[] = [
      { type: 'remove', id: 'source' },
      { type: 'position', id: 'source', position: { x: 5, y: 5 } },
      { type: 'dimensions', id: 'source', dimensions: { width: 1, height: 1 } },
    ];
    expect(selectionAfter('source', changes)).toBe('source');
  });
});

describe('the selection in the side panel', () => {
  it('names the selected block', () => {
    const html = renderToStaticMarkup(
      <SelectionSummary
        pipeline={graphFixtures['every-block-type']}
        selected="step:only-paid"
      />,
    );
    expect(html).toContain('data-selected="step:only-paid"');
    expect(html).toContain('Filter');
    expect(html).toContain('only-paid');
  });

  it('says when nothing is selected', () => {
    const html = renderToStaticMarkup(
      <SelectionSummary
        pipeline={graphFixtures['every-block-type']}
        selected={null}
      />,
    );
    expect(html).toContain('No block selected.');
  });

  it('is in the shell side panel when a pipeline is open', () => {
    const html = renderToStaticMarkup(
      <Shell
        state={{ status: 'connected' }}
        client={null}
        pipeline={graphFixtures['zero-processing-steps']}
      />,
    );
    expect(html).toMatch(
      /aria-label="Side panel"><p class="muted" data-selected="">No block selected/,
    );
    expect(html).toContain('data-pipeline="orders-to-billing"');
    expect(html).not.toContain('No pipeline open.');
  });

  it('is not drawn without a pipeline', () => {
    const html = renderToStaticMarkup(
      <Shell state={{ status: 'connected' }} client={null} />,
    );
    expect(html).toContain('No pipeline open.');
    expect(html).not.toContain('data-selected');
  });
});
