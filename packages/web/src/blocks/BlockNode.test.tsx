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
import type { CodeView } from './source-buffer.ts';
import {
  codeView,
  codeWords,
  edited,
  loadedSource,
  sourceRefusalWords,
} from './source-buffer.ts';

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

// The code panel a custom-code node holds (#14).

const step = blockOf('customCode');
const where = {
  kind: 'file',
  path: 'src/discounts.go',
  symbol: 'ApplyDiscounts',
} as const;
const readFile = (text: string, generated = false) =>
  loadedSource({ text, sha256: 'a'.repeat(64), generated });
const drawCode = (code: CodeView, scheme: Scheme = 'light') =>
  inScheme(scheme, <CustomCodeNode block={step} code={code} />);
const panelOf = (html: string) =>
  /<section class="code-panel".*<\/section>/s.exec(html)?.[0] ?? '';

describe('the code panel of a custom-code node', () => {
  const views: Record<string, CodeView> = {
    'an opened file': codeView(
      where,
      readFile('func ApplyDiscounts() {}\n'),
      'go',
      null,
    ),
    'an empty file': codeView(where, readFile(''), 'go', null),
    'a missing file': codeView(where, loadedSource(null), 'go', null),
    'a generated file': codeView(
      where,
      readFile('package gen\n', true),
      'go',
      null,
    ),
    'unsaved code': codeView(where, edited(readFile('a\n'), 'b\n'), 'go', null),
    'a refused read': codeView(
      where,
      { status: 'refused', error: { error: 'link', message: 'link' } },
      'go',
      null,
    ),
    'no reference': codeView({ kind: 'none' }, undefined, 'go', null),
    'no file to read': { kind: 'noFile' },
  };

  for (const [name, view] of Object.entries(views)) {
    for (const scheme of schemes) {
      it(`renders ${name} to a string in the ${scheme} scheme`, () => {
        const html = drawCode(view, scheme);
        expect(html).toContain(`color-scheme:${scheme}`);
        // The card is drawn as it always is, the panel after it.
        expect(html).toContain(
          '<div class="block-node" data-type="customCode"',
        );
        expect(html).toMatch(/<\/div><section class="code-panel"/);
        expect(html).toMatchSnapshot();
      });
    }
  }

  it('mounts the editor only in an effect: a string render has its empty host and no text', () => {
    const html = drawCode(views['an opened file'] as CodeView);
    expect(html).toContain(
      '<div class="code-editor nodrag nopan nowheel nokey" data-language="go"></div>',
    );
    expect(html).not.toContain('func ApplyDiscounts');
  });

  it('names the file and the symbol above the editor', () => {
    const html = drawCode(views['an opened file'] as CodeView);
    expect(html).toMatch(/src\/discounts\.go.*ApplyDiscounts.*code-editor/s);
  });

  it('says a block with no reference names no file, with no editor', () => {
    const html = panelOf(drawCode(views['no reference'] as CodeView));
    expect(html).toContain(codeWords.noReference);
    expect(html).not.toContain('code-editor');
  });

  it('says there is no file to read without a code view, and draws no editor', () => {
    const html = renderToStaticMarkup(<CustomCodeNode block={step} />);
    expect(html).toContain(codeWords.noFile);
    expect(html).not.toContain('code-editor');
    expect(renderToStaticMarkup(<BlockNodeView block={step} />)).toBe(html);
  });

  it('shows an empty file as an editor, and a missing one as one marked not existing yet', () => {
    const empty = panelOf(drawCode(views['an empty file'] as CodeView));
    expect(empty).toContain('code-editor');
    expect(empty).not.toContain(codeWords.missing);
    const missing = panelOf(drawCode(views['a missing file'] as CodeView));
    expect(missing).toContain('code-editor');
    expect(missing).toContain(codeWords.missing);
    expect(missing).toContain('>Create file</button>');
  });

  it('shows a generated file read-only, with no save control', () => {
    const html = panelOf(drawCode(views['a generated file'] as CodeView));
    expect(html).toContain(codeWords.generated);
    expect(html).toContain('data-readonly=""');
    expect(html).not.toContain('>Save</button>');
  });

  it("shows a refusal's sentence and no editor", () => {
    const html = panelOf(drawCode(views['a refused read'] as CodeView));
    expect(html).toContain(sourceRefusalWords.link);
    expect(html).not.toContain('code-editor');
    expect(html).toContain('>Reload</button>');
  });

  it('marks unsaved code, and enables Save only then', () => {
    const dirty = drawCode(views['unsaved code'] as CodeView);
    expect(dirty).toContain('class="custom-code-node" data-unsaved=""');
    expect(dirty).toContain(`>${codeWords.unsaved}</span>`);
    expect(dirty).toMatch(
      /<button type="button" class="nodrag">Save<\/button>/,
    );
    const clean = drawCode(views['an opened file'] as CodeView);
    expect(clean).not.toContain('data-unsaved');
    expect(clean).toMatch(/<button[^>]*disabled=""[^>]*>Save<\/button>/);
  });

  it('disables Save while disconnected, with the connection state as the reason', () => {
    const offline = codeView(
      where,
      edited(readFile('a\n'), 'b\n'),
      'go',
      'Agent not reachable',
    );
    const html = drawCode(offline);
    const reason =
      /<button[^>]*disabled=""[^>]*aria-describedby="([^"]+)"[^>]*>Save/.exec(
        html,
      )?.[1];
    expect(reason).toBeDefined();
    expect(html).toContain(
      `id="${reason}">Save unavailable: Agent not reachable</span>`,
    );
  });

  it('says the file changed on disk and offers to reload, never to overwrite', () => {
    const view = codeView(
      where,
      { ...edited(readFile('a\n'), 'b\n'), failure: { kind: 'changed' } },
      'go',
      null,
    );
    const html = drawCode(view);
    expect(html).toContain(codeWords.changed);
    expect(html).toContain('>Reload</button>');
    expect(html).not.toMatch(/overwrite/i);
  });

  it('hangs below the card at its width, and draws larger over the canvas when expanded', () => {
    expect(declarations('.custom-code-node')).toMatchObject({
      position: 'relative',
      width: '100%',
      height: '100%',
    });
    expect(declarations('.code-panel')).toMatchObject({
      position: 'absolute',
      top: 'calc(100% + 0.375rem)',
      left: '0',
      width: '100%',
    });
    expect(declarations('.code-panel[data-expanded]')).toMatchObject({
      width: '720px',
    });
    expect(
      declarations('.code-panel[data-expanded] .code-editor'),
    ).toMatchObject({ height: '480px' });
    expect(
      declarations('.react-flow__node:has(.code-panel[data-expanded])'),
    ).toMatchObject({ 'z-index': '1000 !important' });
    // A long line scrolls inside: the editor never grows past its box.
    expect(declarations('.code-editor')).toMatchObject({
      width: '100%',
      'min-width': '0',
      overflow: 'hidden',
    });
    expect(declarations('.code-panel .code-editor .cm-scroller')).toMatchObject(
      {
        overflow: 'auto',
      },
    );
  });
});

describe('the code panel in both schemes', () => {
  const panel = declarations('.code-panel');
  const tokenRules = rules().filter((rule) =>
    rule.selector.startsWith('.code-panel .tok-'),
  );

  it('colours the highlighting it has', () => {
    expect(tokenRules.map((rule) => rule.selector)).toEqual(
      expect.arrayContaining([
        '.code-panel .tok-keyword',
        '.code-panel .tok-string',
        '.code-panel .tok-comment',
        '.code-panel .tok-number',
        '.code-panel .tok-typeName',
      ]),
    );
  });

  for (const scheme of schemes) {
    const surface = inSchemeColour(panel['--code-surface'], scheme);
    const ink = inSchemeColour(panel['--code-ink'], scheme);

    it(`keeps the code, its gutter and every token readable in the ${scheme} scheme`, () => {
      expect(contrast(ink, surface)).toBeGreaterThanOrEqual(4.5);
      expect(
        contrast(inSchemeColour(panel['--code-muted'], scheme), surface),
      ).toBeGreaterThanOrEqual(4.5);
      expect(
        contrast(
          inSchemeColour(panel['--code-muted'], scheme),
          inSchemeColour(panel['--code-gutter'], scheme),
        ),
      ).toBeGreaterThanOrEqual(4.5);
      expect(
        contrast(ink, inSchemeColour(panel['--code-selection'], scheme)),
      ).toBeGreaterThanOrEqual(4.5);
      expect(
        contrast(inSchemeColour(panel['--code-border'], scheme), surface),
      ).toBeGreaterThanOrEqual(3);
      for (const rule of tokenRules) {
        expect(
          contrast(inSchemeColour(rule.declarations.color, scheme), surface),
          rule.selector,
        ).toBeGreaterThanOrEqual(4.5);
      }
    });
  }
});

describe('a custom-code node whose file has staged changes (#43)', () => {
  const view = codeView(
    where,
    readFile('func ApplyDiscounts() {}\n'),
    'go',
    null,
  );
  const actions = {
    onChange: () => undefined,
    onSave: () => undefined,
    onReload: () => undefined,
  };

  it('says so in the fixed sentence, with a control that opens the review', () => {
    const html = renderToStaticMarkup(
      <CustomCodeNode
        block={step}
        code={view}
        actions={{
          ...actions,
          stagedPaths: ['src/discounts.go'],
          onReview: () => undefined,
        }}
      />,
    );
    expect(html).toContain(codeWords.staged);
    expect(codeWords.staged).toBe(
      'This file has staged changes waiting for review',
    );
    expect(html).toContain('Review staged changes');
  });

  it('says nothing for a file with nothing staged', () => {
    const html = renderToStaticMarkup(
      <CustomCodeNode
        block={step}
        code={view}
        actions={{ ...actions, stagedPaths: ['src/other.go'] }}
      />,
    );
    expect(html).not.toContain(codeWords.staged);
    expect(html).not.toContain('Review staged changes');
  });
});
