import type { ReactNode } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';

import { graphFixtures } from '../graph/fixtures.ts';
import { placedBlocks } from '../graph/layout.ts';
import type { BlockKind, DrawableBlock } from './BlockNode.tsx';
import {
  BlockNodeView,
  blockKindLabel,
  blockKinds,
  CustomCodeNode,
  FilterNode,
  KafkaDestinationNode,
  KafkaSourceNode,
  TransformNode,
  UnsupportedCodeNode,
  ValidateNode,
} from './BlockNode.tsx';
import css from './blocks.css?raw';

// One block of every type, straight from the pipeline model: the shared
// every-block-type fixture, validated by @kpv/schema. The unsupported-code
// block is not in the model yet (#42), so it is made here.
const modelBlocks = placedBlocks(graphFixtures['every-block-type']).map(
  (placed) => placed.block,
);
const blockOf = <Kind extends BlockKind>(kind: Kind) => {
  if (kind === 'unsupportedCode') {
    return { type: 'unsupportedCode', id: 'legacy-enrichment' } as Extract<
      DrawableBlock,
      { type: Kind }
    >;
  }
  const found = modelBlocks.find((block) => block.type === kind);
  if (found === undefined) throw new Error(`no ${kind} block in the fixture`);
  return found as Extract<DrawableBlock, { type: Kind }>;
};

type Scheme = 'light' | 'dark';
const schemes: Scheme[] = ['light', 'dark'];

/** A node drawn in one colour scheme, the way the page sets one. */
const inScheme = (scheme: Scheme, node: ReactNode) =>
  renderToStaticMarkup(<div style={{ colorScheme: scheme }}>{node}</div>);

/** Each type's own component, as the graph would use it. */
const ownComponent: Record<BlockKind, (status?: ReactNode) => ReactNode> = {
  kafkaSource: (status) => (
    <KafkaSourceNode block={blockOf('kafkaSource')} status={status} />
  ),
  filter: (status) => <FilterNode block={blockOf('filter')} status={status} />,
  transform: (status) => (
    <TransformNode block={blockOf('transform')} status={status} />
  ),
  validate: (status) => (
    <ValidateNode block={blockOf('validate')} status={status} />
  ),
  customCode: (status) => (
    <CustomCodeNode block={blockOf('customCode')} status={status} />
  ),
  kafkaDestination: (status) => (
    <KafkaDestinationNode block={blockOf('kafkaDestination')} status={status} />
  ),
  unsupportedCode: (status) => (
    <UnsupportedCodeNode block={blockOf('unsupportedCode')} status={status} />
  ),
};

const iconOf = (html: string): string => {
  const match = /<span class="block-icon"[^>]*>(.*?)<\/span>/.exec(html);
  if (match?.[1] === undefined) throw new Error(`no icon in ${html}`);
  return match[1];
};

describe('the block node components', () => {
  it('has one for each block type, the unsupported-code block among them', () => {
    expect(blockKinds).toEqual([
      'kafkaSource',
      'filter',
      'transform',
      'validate',
      'customCode',
      'kafkaDestination',
      'unsupportedCode',
    ]);
  });

  for (const kind of blockKinds) {
    for (const scheme of schemes) {
      it(`draws ${kind} in the ${scheme} scheme`, () => {
        const html = inScheme(scheme, ownComponent[kind]());
        expect(html).toContain(`color-scheme:${scheme}`);
        expect(html).toContain(`data-type="${kind}"`);
        expect(html).toContain(
          `<span class="block-type">${blockKindLabel[kind]}</span>`,
        );
        const name = blockOf(kind).id as string;
        expect(html).toContain(`title="${name}">${name}</span>`);
        expect(html).toContain('<svg');
        expect(html).toMatchSnapshot();
      });
    }

    it(`draws ${kind} the same through BlockNodeView as through its own component`, () => {
      expect(
        renderToStaticMarkup(<BlockNodeView block={blockOf(kind)} />),
      ).toBe(renderToStaticMarkup(ownComponent[kind]()));
    });
  }

  it('tells every type apart without colour: its own icon shape and its own words', () => {
    const icons = blockKinds.map((kind) =>
      iconOf(renderToStaticMarkup(ownComponent[kind]())),
    );
    expect(new Set(icons).size).toBe(blockKinds.length);
    const labels = blockKinds.map((kind) => blockKindLabel[kind]);
    expect(new Set(labels).size).toBe(blockKinds.length);
  });

  it('labels the whole node with its type and name for a screen reader, and hides the icon', () => {
    const html = renderToStaticMarkup(ownComponent.filter());
    expect(html).toContain('aria-label="Filter: only-paid"');
    expect(html).toContain('<span class="block-icon" aria-hidden="true">');
  });
});

describe('a long name', () => {
  // As long as an identifier can be.
  const long = `a${'very-long-name-'.repeat(4)}`.padEnd(63, 'x');

  it('is shown whole in the markup with the whole name as its tooltip', () => {
    const html = renderToStaticMarkup(
      <FilterNode block={{ ...blockOf('filter'), id: long }} />,
    );
    expect(html).toContain(`title="${long}">${long}</span>`);
  });

  it('is cut short on one line by the stylesheet, inside a node that keeps its size', () => {
    // The node is exactly the box it is given and clips what is in it.
    expect(declarations('.block-node')).toMatchObject({
      'box-sizing': 'border-box',
      width: '100%',
      height: '100%',
      overflow: 'hidden',
    });
    // The text column may shrink below its content, so the name cannot
    // push the node wider.
    expect(declarations('.block-text')).toMatchObject({
      'min-width': '0',
      flex: '1 1 auto',
    });
    expect(declarations('.block-name')).toMatchObject({
      'white-space': 'nowrap',
      overflow: 'hidden',
      'text-overflow': 'ellipsis',
    });
    expect(declarations('.block-type')).toMatchObject({
      'white-space': 'nowrap',
      overflow: 'hidden',
      'text-overflow': 'ellipsis',
    });
  });
});

describe('a block with no name', () => {
  for (const scheme of schemes) {
    it(`says it is unnamed rather than showing an empty box, in the ${scheme} scheme`, () => {
      const html = inScheme(
        scheme,
        <FilterNode block={{ ...blockOf('filter'), id: '' }} />,
      );
      expect(html).toContain(
        '<span class="block-name" data-fallback="" title="Unnamed filter">Unnamed filter</span>',
      );
      expect(html).toContain('aria-label="Filter: Unnamed filter"');
      expect(html).toMatchSnapshot();
    });
  }

  it('treats a name of only spaces as no name', () => {
    const html = renderToStaticMarkup(
      <KafkaSourceNode block={{ ...blockOf('kafkaSource'), id: '   ' }} />,
    );
    expect(html).toContain('>Unnamed kafka source</span>');
  });

  it('covers an unsupported-code block without an id', () => {
    const html = renderToStaticMarkup(
      <UnsupportedCodeNode block={{ type: 'unsupportedCode' }} />,
    );
    expect(html).toContain('>Unnamed unsupported code</span>');
  });

  it('is told apart from a name by the stylesheet as well', () => {
    expect(declarations('.block-name[data-fallback]')).toMatchObject({
      'font-style': 'italic',
    });
  });
});

describe('the status badge slot', () => {
  for (const kind of blockKinds) {
    it(`is not rendered at all on ${kind} when no status is supplied`, () => {
      for (const none of [undefined, null, false, '']) {
        expect(renderToStaticMarkup(ownComponent[kind](none))).not.toContain(
          'block-status',
        );
      }
    });
  }

  for (const scheme of schemes) {
    it(`renders the status it is handed after the name, in the ${scheme} scheme`, () => {
      const html = inScheme(
        scheme,
        ownComponent.customCode(<span className="badge">running</span>),
      );
      expect(html).toMatch(
        /class="block-name"[^>]*>apply-discounts<\/span><\/span><span class="block-status"><span class="badge">running<\/span><\/span><\/div>/,
      );
      expect(html).toMatchSnapshot();
    });
  }
});

// The stylesheet's colours, checked in both schemes. A node's colours are
// light-dark() pairs: the first is the light scheme's, the second the dark's.

/** The declarations of one rule of blocks.css, by its exact selector. */
function declarations(selector: string): Record<string, string> {
  const rule = rules().find((found) => found.selector === selector);
  if (rule === undefined) throw new Error(`blocks.css has no ${selector}`);
  return rule.declarations;
}

function rules(): {
  selector: string;
  declarations: Record<string, string>;
}[] {
  const text = css.replace(/\/\*[\s\S]*?\*\//g, '');
  return [...text.matchAll(/([^{}]+)\{([^}]*)\}/g)].map((match) => ({
    selector: (match[1] ?? '').trim(),
    declarations: Object.fromEntries(
      (match[2] ?? '')
        .split(';')
        .map((line) => line.trim())
        .filter((line) => line !== '')
        .map((line) => {
          const colon = line.indexOf(':');
          return [line.slice(0, colon).trim(), line.slice(colon + 1).trim()];
        }),
    ),
  }));
}

/** A light-dark() colour pair's value in one scheme. */
function inSchemeColour(value: string | undefined, scheme: Scheme): string {
  const match = /^light-dark\((#[0-9a-f]{6}), (#[0-9a-f]{6})\)$/.exec(
    value ?? '',
  );
  if (match === null) throw new Error(`${value} is not a light-dark() pair`);
  return (scheme === 'light' ? match[1] : match[2]) as string;
}

/** WCAG 2 contrast ratio of two #rrggbb colours. */
function contrast(a: string, b: string): number {
  const luminance = (hex: string) => {
    const [r, g, b] = [1, 3, 5].map((at) => {
      const c = parseInt(hex.slice(at, at + 2), 16) / 255;
      return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
    }) as [number, number, number];
    return 0.2126 * r + 0.7152 * g + 0.0722 * b;
  };
  const [high, low] = [luminance(a), luminance(b)].sort((x, y) => y - x) as [
    number,
    number,
  ];
  return (high + 0.05) / (low + 0.05);
}

const accentOf = (kind: BlockKind, scheme: Scheme) =>
  inSchemeColour(
    declarations(`.block-node[data-type='${kind}']`)['--block-accent'],
    scheme,
  );

describe('legibility in both schemes', () => {
  for (const scheme of schemes) {
    const base = declarations('.block-node');
    const surface = inSchemeColour(base['--block-surface'], scheme);

    it(`keeps the name and the type label readable in the ${scheme} scheme`, () => {
      // WCAG AA for text.
      expect(
        contrast(inSchemeColour(base['--block-ink'], scheme), surface),
      ).toBeGreaterThanOrEqual(4.5);
      expect(
        contrast(inSchemeColour(base['--block-muted'], scheme), surface),
      ).toBeGreaterThanOrEqual(4.5);
    });

    it(`keeps the node's edge and every type's icon visible in the ${scheme} scheme`, () => {
      // WCAG AA for non-text graphics.
      expect(
        contrast(inSchemeColour(base['--block-border'], scheme), surface),
      ).toBeGreaterThanOrEqual(3);
      for (const kind of blockKinds) {
        expect(
          contrast(accentOf(kind, scheme), surface),
          `${kind} accent`,
        ).toBeGreaterThanOrEqual(3);
      }
    });

    it(`gives each type its own colour in the ${scheme} scheme`, () => {
      const accents = blockKinds.map((kind) => accentOf(kind, scheme));
      expect(new Set(accents).size).toBe(blockKinds.length);
    });
  }

  it('draws the unsupported-code block dashed, so it differs in shape too', () => {
    expect(
      declarations(".block-node[data-type='unsupportedCode']"),
    ).toMatchObject({ 'border-style': 'dashed' });
  });
});
