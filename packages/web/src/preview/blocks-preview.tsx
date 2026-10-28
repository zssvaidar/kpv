// A development page that draws every block node component side by side in
// the light and the dark colour scheme, at the size the graph gives a node:
// the components' story. Each type is drawn with its name from the
// every-block-type fixture, then with a long name, with no name, and with a
// status badge. `pnpm dev` serves it at /blocks-preview.html; a build leaves
// it out, because only index.html is a build input. It talks to no agent.

import type { ReactNode } from 'react';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';

import type { DrawableBlock } from '../blocks/BlockNode.tsx';
import { BlockNodeView } from '../blocks/BlockNode.tsx';
import { graphFixtures } from '../graph/fixtures.ts';
import { nodeHeight, nodeWidth, placedBlocks } from '../graph/layout.ts';

const blocks: DrawableBlock[] = [
  ...placedBlocks(graphFixtures['every-block-type']).map(
    (placed) => placed.block,
  ),
  { type: 'unsupportedCode', id: 'legacy-enrichment' },
];

const variants: { name: string; draw: (block: DrawableBlock) => ReactNode }[] =
  [
    { name: 'named', draw: (block) => <BlockNodeView block={block} /> },
    {
      name: 'long name',
      draw: (block) => (
        <BlockNodeView
          block={{ ...block, id: 'a-very-long-block-name-that-cannot-fit' }}
        />
      ),
    },
    {
      name: 'no name',
      draw: (block) => <BlockNodeView block={{ ...block, id: '' }} />,
    },
    {
      name: 'with status',
      draw: (block) => (
        <BlockNodeView
          block={block}
          status={<span className="preview-badge">running</span>}
        />
      ),
    },
  ];

const cell = { width: nodeWidth, height: nodeHeight };

function Board(props: { scheme: 'light' | 'dark' }) {
  return (
    <section
      data-scheme={props.scheme}
      style={{
        colorScheme: props.scheme,
        background: 'Canvas',
        color: 'CanvasText',
        padding: '1rem',
        fontFamily: 'system-ui, sans-serif',
      }}
    >
      <h2 style={{ margin: '0 0 0.5rem', fontSize: '1rem' }}>{props.scheme}</h2>
      <table style={{ borderSpacing: '0.75rem' }}>
        <thead>
          <tr>
            {variants.map((variant) => (
              <th key={variant.name} style={{ fontWeight: 'normal' }}>
                {variant.name}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {blocks.map((block) => (
            <tr key={block.type}>
              {variants.map((variant) => (
                <td key={variant.name}>
                  <div className="preview-cell" style={cell}>
                    {variant.draw(block)}
                  </div>
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}

const root = document.getElementById('root');
if (root === null) {
  throw new Error('blocks-preview.html has no #root element');
}
createRoot(root).render(
  <StrictMode>
    <style>{`.preview-badge { padding: 0 0.375rem; border-radius: 1rem; border: 1px solid currentColor; }`}</style>
    <Board scheme="light" />
    <Board scheme="dark" />
  </StrictMode>,
);
