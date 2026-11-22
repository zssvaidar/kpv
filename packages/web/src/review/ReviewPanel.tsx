// The review of an opened pipeline's staged changes (#43): every staged file
// by path with its line diff, Apply and Discard, the last report, and the
// snapshots with Restore and Remove. It draws the state its controller owns
// and reports what the person activates; it sends nothing itself.

import { useId } from 'react';

import './review.css';

import { problemWords } from '../blocks/source-buffer.ts';
import type { ReviewController, ReviewState } from './controller.ts';
import { reviewControls } from './controls.ts';
import type { DiffRow } from './diff.ts';
import { changeSentence, endingLabel, fileDiff } from './diff.ts';
import {
  discardStagedQuestion,
  removeSnapshotQuestion,
  resultWords,
  reviewErrorWords,
  reviewWords,
  stateWords,
} from './words.ts';

type ReviewActions = Pick<
  ReviewController,
  'apply' | 'restore' | 'discard' | 'removeSnapshot' | 'hide'
>;

/** A snapshot's time as the review shows it: UTC, to the second. */
export const takenWords = (taken: string): string =>
  `${taken.replace('T', ' ').replace(/\.\d+/, '').replace('Z', '')} UTC`;

function DiffTable(props: { rows: DiffRow[] }) {
  if (props.rows.length === 0) {
    return <p className="review-message">The file is empty.</p>;
  }
  return (
    <table className="review-diff">
      <tbody>
        {props.rows.map((row, index) => (
          <tr key={index} data-kind={row.kind}>
            <td className="review-number">
              {row.kind === 'added' ? '' : row.base}
            </td>
            <td className="review-number">
              {row.kind === 'removed' ? '' : row.staged}
            </td>
            <td className="review-marker">
              {row.kind === 'removed' ? '-' : row.kind === 'added' ? '+' : ' '}
            </td>
            <td className="review-line">
              <code>{row.line.text}</code>
              {row.kind === 'same' ? null : (
                <span className="review-ending">
                  {endingLabel[row.line.ending]}
                </span>
              )}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

export function ReviewPanel(props: {
  state: ReviewState;
  actions: ReviewActions;
  /** The connection state's words while disconnected, else null. */
  offline: string | null;
  confirm: (question: string) => boolean;
}) {
  const { state, actions, offline, confirm } = props;
  const reasonId = useId();
  const review = state.review;
  const controls = reviewControls(review, offline, state.busy !== null);
  const count = review?.files.length ?? 0;
  const report = state.report;
  return (
    <section className="review" aria-label={reviewWords.title}>
      <div className="review-head">
        <h2>{reviewWords.title}</h2>
        <span className="review-count">{reviewWords.staged(count)}</span>
        <button type="button" onClick={() => actions.hide()}>
          Close
        </button>
      </div>
      {state.loading ? (
        <p className="review-message">Reading the review.</p>
      ) : null}
      {state.problem === null ? null : (
        <p className="review-message" role="alert">
          {problemWords(state.problem)}
        </p>
      )}
      {state.readFailure === null ? null : (
        <p className="review-message" role="alert">
          {reviewErrorWords[state.readFailure.error]}
        </p>
      )}
      {review?.unreadable === true ? (
        <p className="review-message" role="alert">
          {reviewWords.unreadable}
        </p>
      ) : null}
      {state.failure === null ? null : (
        <div className="review-failure" role="alert">
          <p>{reviewErrorWords[state.failure.error.error]}</p>
          {state.failure.error.drift === undefined ? null : (
            <>
              <p>{reviewWords.driftTitle}</p>
              <ul>
                {state.failure.error.drift.map((drift) => (
                  <li key={drift.path}>
                    <code>{drift.path}</code>:{' '}
                    {drift.now === 'ready' ? '' : stateWords[drift.now]}
                  </li>
                ))}
              </ul>
            </>
          )}
        </div>
      )}
      {report === null ? null : (
        <div className="review-report" role="status">
          <p>
            {report.action === 'apply'
              ? report.report.complete
                ? reviewWords.applied
                : reviewWords.appliedPartly
              : report.report.complete
                ? reviewWords.restored
                : reviewWords.restoredPartly}
          </p>
          <ul>
            {report.report.files.map((file) => (
              <li key={file.path} data-result={file.result}>
                <code>{file.path}</code>: {resultWords[file.result]}
                {file.kind === undefined
                  ? ''
                  : ` (${reviewErrorWords[file.kind]})`}
              </li>
            ))}
          </ul>
          {report.action === 'apply' && !report.report.complete ? (
            <button
              type="button"
              disabled={!controls.snapshot.enabled}
              onClick={() => actions.restore(report.report.snapshot)}
            >
              Restore the snapshot
            </button>
          ) : null}
        </div>
      )}
      {review === null ? null : count === 0 ? (
        <p className="review-message">{reviewWords.nothingStaged}</p>
      ) : (
        <ul className="review-files">
          {review.files.map((file) => {
            const rows = fileDiff(file);
            return (
              <li
                key={file.path}
                data-change={file.change}
                data-state={file.state}
              >
                <h3>
                  <code>{file.path}</code> <span>{changeSentence(file)}</span>
                </h3>
                {rows === null ? (
                  <p className="review-message" role="alert">
                    {file.state === 'ready'
                      ? reviewWords.noDiff
                      : stateWords[file.state]}
                  </p>
                ) : (
                  <DiffTable rows={rows} />
                )}
              </li>
            );
          })}
        </ul>
      )}
      <div className="review-actions">
        <button
          type="button"
          disabled={!controls.apply.enabled}
          aria-describedby={
            controls.apply.reason === null ? undefined : reasonId
          }
          onClick={() => actions.apply()}
        >
          Apply
        </button>
        <button
          type="button"
          disabled={!controls.discard.enabled}
          onClick={() => {
            if (confirm(discardStagedQuestion(count))) actions.discard();
          }}
        >
          Discard
        </button>
        {controls.apply.reason === null ? null : (
          <span className="review-reason" id={reasonId}>
            {controls.apply.reason}
          </span>
        )}
      </div>
      <h3>{reviewWords.snapshots}</h3>
      {review === null || review.snapshots.length === 0 ? (
        <p className="review-message">{reviewWords.noSnapshots}</p>
      ) : (
        <ul className="review-snapshots">
          {review.snapshots.map((snapshot) => (
            <li key={snapshot.id}>
              <span>{takenWords(snapshot.taken)}</span>:{' '}
              {snapshot.paths.map((path, index) => (
                <span key={path}>
                  {index === 0 ? '' : ', '}
                  <code>{path}</code>
                </span>
              ))}{' '}
              <button
                type="button"
                disabled={!controls.snapshot.enabled}
                onClick={() => actions.restore(snapshot.id)}
              >
                Restore
              </button>
              <button
                type="button"
                disabled={!controls.snapshot.enabled}
                onClick={() => {
                  if (
                    confirm(removeSnapshotQuestion(takenWords(snapshot.taken)))
                  ) {
                    actions.removeSnapshot(snapshot.id);
                  }
                }}
              >
                Remove
              </button>
            </li>
          ))}
        </ul>
      )}
      <p className="review-message">{reviewWords.emptyDirectories}</p>
    </section>
  );
}
