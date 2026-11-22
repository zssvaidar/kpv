// The block node components: what one block of a pipeline looks like on the
// graph. There is one for each block type, plus a variant for the opaque
// unsupported-code block an imported pipeline wraps code it cannot map in.
//
// They are presentational only. Each takes its block as a prop, straight from
// the pipeline model, and an optional status badge from whoever knows the
// status (#35); none of them fetches anything or talks to the agent, and none
// of them knows about the graph library. The graph puts one inside each of
// its nodes (graph/PipelineGraph.tsx), with the handles its edges attach to.
//
// Every node shows the same three things: an icon and a label that say what
// type of block it is, the block's name, and the status badge when there is
// one. The type is told apart by the icon's shape and the label's words as
// well as by colour, so it reads the same to someone who cannot tell the
// colours apart. A node fills the box the graph gives it and nothing in it can
// grow that box: a long name is cut short with an ellipsis (the whole name is
// its tooltip), and a missing one is replaced by words saying so.
//
// A custom-code node also holds the code its step's file has (#14), in a
// panel hanging below the card at the card's width, so the card keeps the
// footprint every node has and the edges still meet it at its middle. The
// panel is controlled like the rest: it is handed what to show (a CodeView,
// source-buffer.ts) and reports what is typed, saved or reloaded; the shell
// reads and writes the file. Without a CodeView it says there is no file to
// read, which is what a pipeline not opened from disk shows.

import './blocks.css';

import type { ReactNode } from 'react';
import { useId, useState } from 'react';

import type {
  CustomCodeStep,
  FilterStep,
  KafkaDestinationBlock,
  KafkaSourceBlock,
  TransformStep,
  ValidateStep,
} from '@kpv/schema';

import { CodeEditor } from './CodeEditor.tsx';
import type { CodeView } from './source-buffer.ts';
import { codeWords } from './source-buffer.ts';

/**
 * The opaque block an imported pipeline wraps unrecognised code in, keeping
 * the code as it was. The pipeline model does not have it yet: mapping a
 * parse into a model is #42, and that is where it joins the schema. Until
 * then this is the least a node needs to draw one: the block's id when it
 * has one.
 */
export interface UnsupportedCodeBlock {
  type: 'unsupportedCode';
  id?: string;
}

/** Any block a node can draw. */
export type DrawableBlock =
  | KafkaSourceBlock
  | FilterStep
  | TransformStep
  | ValidateStep
  | CustomCodeStep
  | KafkaDestinationBlock
  | UnsupportedCodeBlock;

/** The type of a block a node can draw. */
export type BlockKind = DrawableBlock['type'];

/** Every block type, in the order a pipeline has them. */
export const blockKinds = [
  'kafkaSource',
  'filter',
  'transform',
  'validate',
  'customCode',
  'kafkaDestination',
  'unsupportedCode',
] as const satisfies readonly BlockKind[];

/** The words a node uses for its block's type. */
export const blockKindLabel: Record<BlockKind, string> = {
  kafkaSource: 'Kafka source',
  filter: 'Filter',
  transform: 'Transform',
  validate: 'Validate',
  customCode: 'Custom code',
  kafkaDestination: 'Kafka destination',
  unsupportedCode: 'Unsupported code',
};

/**
 * The name a node shows: the block's id, which the model keeps human-readable
 * for this. The schema requires one on every block it has, but a block being
 * edited (#11) or an opaque block may be without it, and a node never shows
 * an empty box for that.
 */
export function blockName(block: { type: BlockKind; id?: string }): {
  text: string;
  fallback: boolean;
} {
  const id = block.id?.trim() ?? '';
  if (id !== '') return { text: id, fallback: false };
  return {
    text: `Unnamed ${blockKindLabel[block.type].toLowerCase()}`,
    fallback: true,
  };
}

/**
 * Whether a status was supplied. Nothing, `false` and the empty string are
 * no status, the way React renders them as nothing.
 */
const hasStatus = (status: ReactNode): boolean =>
  status !== undefined && status !== null && status !== false && status !== '';

/** The props every block node takes: its block, and a status if there is one. */
export interface BlockNodeProps<Block extends DrawableBlock> {
  block: Block;
  /**
   * The status badge, from the runtime status (#35). Without one the node
   * renders no badge slot at all, so it leaves no gap behind.
   */
  status?: ReactNode;
}

/** The shape every node shares. */
function BlockCard(props: BlockNodeProps<DrawableBlock>) {
  const { block, status } = props;
  const name = blockName(block);
  const label = blockKindLabel[block.type];
  return (
    <div
      className="block-node"
      data-type={block.type}
      aria-label={`${label}: ${name.text}`}
    >
      <span className="block-icon" aria-hidden="true">
        {blockIcon[block.type]}
      </span>
      <span className="block-text">
        <span className="block-type">{label}</span>
        <span
          className="block-name"
          data-fallback={name.fallback ? '' : undefined}
          title={name.text}
        >
          {name.text}
        </span>
      </span>
      {hasStatus(status) ? (
        <span className="block-status">{status}</span>
      ) : null}
    </div>
  );
}

export function KafkaSourceNode(props: BlockNodeProps<KafkaSourceBlock>) {
  return <BlockCard {...props} />;
}

export function FilterNode(props: BlockNodeProps<FilterStep>) {
  return <BlockCard {...props} />;
}

export function TransformNode(props: BlockNodeProps<TransformStep>) {
  return <BlockCard {...props} />;
}

export function ValidateNode(props: BlockNodeProps<ValidateStep>) {
  return <BlockCard {...props} />;
}

/** What a custom-code node reports up about its code; the shell acts. */
export interface CodeActions {
  onChange: (path: string, text: string) => void;
  onSave: (path: string) => void;
  onReload: (path: string) => void;
  /**
   * The paths the tool has staged a change to (#43), which the node says in
   * a fixed sentence with a control that opens the review. The editor still
   * shows the file as it is on disk.
   */
  stagedPaths?: readonly string[];
  /** Opens the review of the staged changes. */
  onReview?: () => void;
}

export interface CustomCodeNodeProps extends BlockNodeProps<CustomCodeStep> {
  /** What its code panel shows; no file to read when there is none. */
  code?: CodeView;
  actions?: CodeActions;
}

/** Nothing to report to: a node drawn with no owner of its code. */
const noActions: CodeActions = {
  onChange: () => undefined,
  onSave: () => undefined,
  onReload: () => undefined,
};

export function CustomCodeNode(props: CustomCodeNodeProps) {
  const { code = { kind: 'noFile' }, actions = noActions, ...card } = props;
  return (
    <div
      className="custom-code-node"
      data-unsaved={code.kind === 'editor' && code.unsaved ? '' : undefined}
    >
      <BlockCard {...card} />
      <CodePanel code={code} actions={actions} />
    </div>
  );
}

/**
 * The code below a custom-code card. Expanded, it is drawn larger over the
 * canvas (blocks.css says the size) without moving any other node; it stays
 * the same editor either way, so its text, cursor, selection and undo
 * history are kept.
 */
function CodePanel(props: { code: CodeView; actions: CodeActions }) {
  const { code, actions } = props;
  const [expanded, setExpanded] = useState(false);
  const reasonId = useId();
  if (code.kind === 'noReference' || code.kind === 'noFile') {
    return (
      <section className="code-panel" data-state={code.kind}>
        <p className="code-message">{codeWords[code.kind]}</p>
      </section>
    );
  }
  const { path } = code;
  const editor = code.kind === 'editor' ? code : null;
  return (
    <section
      className="code-panel"
      data-state={code.kind}
      data-expanded={expanded ? '' : undefined}
      aria-label={`Code in ${path}`}
    >
      <div className="code-head">
        <code className="code-path" title={path}>
          {path}
        </code>
        {editor?.unsaved === true ? (
          <span className="code-unsaved">{codeWords.unsaved}</span>
        ) : null}
        {editor === null ? null : (
          <button
            type="button"
            className="code-expand nodrag"
            aria-pressed={expanded}
            onClick={() => setExpanded(!expanded)}
          >
            {expanded ? 'Collapse' : 'Expand'}
          </button>
        )}
      </div>
      {code.symbol === undefined ? null : (
        <p className="code-symbol">
          Symbol <code>{code.symbol}</code>
        </p>
      )}
      {actions.stagedPaths?.includes(path) === true ? (
        <p className="code-message code-staged" role="status">
          {codeWords.staged}{' '}
          {actions.onReview === undefined ? null : (
            <button
              type="button"
              className="nodrag"
              onClick={() => actions.onReview?.()}
            >
              Review staged changes
            </button>
          )}
        </p>
      ) : null}
      {code.kind === 'loading' ? (
        <p className="code-message">{codeWords.loading}</p>
      ) : null}
      {code.kind === 'refused' ? (
        <p className="code-message" role="alert">
          {code.sentence}
        </p>
      ) : null}
      {editor === null ? null : (
        <>
          {editor.generated ? (
            <p className="code-message">{codeWords.generated}</p>
          ) : null}
          {editor.missing ? (
            <p className="code-message">{codeWords.missing}</p>
          ) : null}
          {editor.notice === null ? null : (
            <p className="code-message" role="alert">
              {editor.notice}
            </p>
          )}
          <CodeEditor
            key={path}
            text={editor.text}
            language={editor.language}
            readOnly={editor.generated}
            label={`Code in ${path}`}
            onChange={(text) => actions.onChange(path, text)}
          />
        </>
      )}
      {code.kind === 'loading' ? null : (
        <div className="code-actions">
          {editor === null || editor.generated ? null : (
            <button
              type="button"
              className="nodrag"
              disabled={editor.saveBlocked !== null}
              aria-describedby={
                editor.saveBlocked === null ? undefined : reasonId
              }
              onClick={() => actions.onSave(path)}
            >
              {editor.missing ? 'Create file' : 'Save'}
            </button>
          )}
          <button
            type="button"
            className="nodrag"
            disabled={editor?.saving === true}
            onClick={() => actions.onReload(path)}
          >
            Reload
          </button>
          {editor === null ||
          editor.generated ||
          editor.saveBlocked === null ? null : (
            <span className="code-reason" id={reasonId}>
              {editor.saveBlocked}
            </span>
          )}
        </div>
      )}
    </section>
  );
}

export function KafkaDestinationNode(
  props: BlockNodeProps<KafkaDestinationBlock>,
) {
  return <BlockCard {...props} />;
}

/** The opaque block: drawn dashed, as code kpv keeps but does not model. */
export function UnsupportedCodeNode(
  props: BlockNodeProps<UnsupportedCodeBlock>,
) {
  return <BlockCard {...props} />;
}

/** The node component for whatever block it is handed. */
export function BlockNodeView(
  props: BlockNodeProps<DrawableBlock> & {
    /** A custom-code block's code, and where what is typed goes. */
    code?: CodeView;
    actions?: CodeActions;
  },
) {
  const { block, status, code, actions } = props;
  switch (block.type) {
    case 'kafkaSource':
      return <KafkaSourceNode block={block} status={status} />;
    case 'filter':
      return <FilterNode block={block} status={status} />;
    case 'transform':
      return <TransformNode block={block} status={status} />;
    case 'validate':
      return <ValidateNode block={block} status={status} />;
    case 'customCode':
      return (
        <CustomCodeNode
          block={block}
          status={status}
          {...(code === undefined ? {} : { code })}
          {...(actions === undefined ? {} : { actions })}
        />
      );
    case 'kafkaDestination':
      return <KafkaDestinationNode block={block} status={status} />;
    case 'unsupportedCode':
      return <UnsupportedCodeNode block={block} status={status} />;
  }
}

/**
 * One icon per type, each a different shape, drawn in the type's colour:
 * a bar with an arrow leaving it (source), a funnel (filter), two opposed
 * arrows (transform), a check mark (validate), angle brackets (custom code),
 * an arrow into a bar (destination) and a question mark (unsupported code).
 */
const icon = (paths: ReactNode) => (
  <svg
    viewBox="0 0 16 16"
    width="16"
    height="16"
    fill="none"
    stroke="currentColor"
    strokeWidth="1.6"
    strokeLinecap="round"
    strokeLinejoin="round"
    focusable="false"
  >
    {paths}
  </svg>
);

const blockIcon: Record<BlockKind, ReactNode> = {
  kafkaSource: icon(<path d="M2.5 3v10M5.5 8h8M10.5 5l3 3-3 3" />),
  filter: icon(<path d="M2 3h12l-4.5 5.5V13l-3 1.5v-6Z" />),
  transform: icon(
    <path d="M2 5.5h11M10.5 3l2.5 2.5L10.5 8M14 10.5H3M5.5 8 3 10.5 5.5 13" />,
  ),
  validate: icon(<path d="m2.5 8.5 3.5 3.5 7.5-8" />),
  customCode: icon(<path d="M5 4 1.5 8 5 12M11 4l3.5 4L11 12M9.5 2.5l-3 11" />),
  kafkaDestination: icon(<path d="M2.5 8h8M7.5 5l3 3-3 3M13.5 3v10" />),
  unsupportedCode: icon(
    <path d="M5.5 5.5a2.5 2.5 0 1 1 3.6 2.2c-.7.4-1.1.9-1.1 1.6v.4M8 12.5v.1" />,
  ),
};
