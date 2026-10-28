import type { NodeChange } from '@xyflow/react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';

import { Shell } from '../shell/App.tsx';
import { graphFixtures } from './fixtures.ts';
import { EditControls } from './EditControls.tsx';
import { editProps, PipelineGraph, selectionAfter } from './PipelineGraph.tsx';
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
      onEdit={() => undefined}
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

  it("draws each node with its block's own component", () => {
    const html = render(null);
    const types = [
      ...html.matchAll(/class="block-node" data-type="([^"]+)"/g),
    ].map((match) => match[1]);
    expect(types).toEqual([
      'kafkaSource',
      'filter',
      'transform',
      'validate',
      'customCode',
      'kafkaDestination',
    ]);
    expect(html).toContain('aria-label="Validate: require-amount"');
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

  it('lets processing steps be dragged and deleted, and nothing be connected', () => {
    expect(editProps).toMatchObject({
      nodesDraggable: true,
      nodesConnectable: false,
      edgesReconnectable: false,
      deleteKeyCode: ['Delete', 'Backspace'],
      connectOnClick: false,
    });
    const html = render(null);
    // Each step is draggable; the source and the destination are not.
    const draggable = [
      ...html.matchAll(
        /class="react-flow__node[^"]*\bdraggable\b[^"]*"[^>]*data-id="([^"]+)"/g,
      ),
    ].map((match) => match[1]);
    expect(draggable).toEqual([
      'step:only-paid',
      'step:to-billing-shape',
      'step:require-amount',
      'step:apply-discounts',
    ]);
    expect(html).not.toMatch(/react-flow__handle[^"]*\bconnectable/);
    // Fit, zoom in and zoom out; no interactivity toggle.
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

  it('offers no editing controls until a block is selected', () => {
    const html = renderToStaticMarkup(
      <Shell
        state={{ status: 'connected' }}
        client={null}
        pipeline={graphFixtures['every-block-type']}
      />,
    );
    expect(html).not.toContain('edit-controls');
  });

  it('is not drawn without a pipeline', () => {
    const html = renderToStaticMarkup(
      <Shell state={{ status: 'connected' }} client={null} />,
    );
    expect(html).toContain('No pipeline open.');
    expect(html).not.toContain('data-selected');
  });
});

describe('the editing controls in the side panel', () => {
  const controls = (selected: string | null) =>
    renderToStaticMarkup(
      <EditControls
        pipeline={graphFixtures['every-block-type']}
        selected={selected}
        onEdit={() => undefined}
      />,
    );
  const inserts = (html: string) =>
    [...html.matchAll(/data-insert="([^"]+)"/g)].map((match) => match[1]);

  it('offers inserting every step type after a step, and moving and deleting it', () => {
    const html = controls('step:to-billing-shape');
    expect(inserts(html)).toEqual([
      'filter',
      'transform',
      'validate',
      'customCode',
    ]);
    expect(html).toContain('Move earlier');
    expect(html).toContain('Move later');
    expect(html).toContain('Delete step');
    expect(html).not.toMatch(/disabled=""[^>]*>Move/);
  });

  it('cannot move the first step earlier or the last step later', () => {
    expect(controls('step:only-paid')).toMatch(/disabled=""[^>]*>Move earlier/);
    expect(controls('step:apply-discounts')).toMatch(
      /disabled=""[^>]*>Move later/,
    );
  });

  it('offers only inserting after the source, and nothing for the destination', () => {
    const atSource = controls('source');
    expect(inserts(atSource)).toHaveLength(4);
    expect(atSource).not.toContain('Delete step');
    expect(atSource).not.toContain('Move');
    expect(controls('destination')).toBe(
      '<div class="edit-controls" aria-label="Edit the pipeline"></div>',
    );
    expect(controls(null)).toBe('');
  });
});
