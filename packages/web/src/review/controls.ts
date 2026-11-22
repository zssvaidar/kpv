// Which of the review's controls are enabled, and why not (#43): pure, so it
// is tested without a DOM. Apply is sent only when the person activates it;
// nothing here or in the controller applies on its own.

import type { WorkcopyReview } from '@kpv/schema';

export interface Control {
  enabled: boolean;
  /** Why it is disabled, or null when it is enabled. */
  reason: string | null;
}

export interface ReviewControls {
  apply: Control;
  discard: Control;
  /** Restoring or removing a snapshot. */
  snapshot: Control;
}

export const controlWords = {
  busy: 'Waiting for the agent',
  nothingStaged: 'Nothing is staged',
  drift:
    'Some project files differ from what was staged, so apply would write nothing',
  unavailable: (offline: string) => `Unavailable: ${offline}`,
} as const;

const enabled: Control = { enabled: true, reason: null };
const disabled = (reason: string): Control => ({ enabled: false, reason });

/**
 * The controls for a review. `offline` is the connection state's words
 * while there is no working connection to the agent, null while there is;
 * `busy` is a request of the review's still unanswered.
 */
export function reviewControls(
  review: WorkcopyReview | null,
  offline: string | null,
  busy: boolean,
): ReviewControls {
  const blocked =
    offline !== null
      ? disabled(controlWords.unavailable(offline))
      : busy
        ? disabled(controlWords.busy)
        : null;
  const staged = review !== null && review.files.length > 0;
  const drifted = review?.files.some((file) => file.state !== 'ready') ?? false;
  return {
    apply:
      blocked ??
      (!staged
        ? disabled(controlWords.nothingStaged)
        : drifted
          ? disabled(controlWords.drift)
          : enabled),
    discard:
      blocked ?? (staged ? enabled : disabled(controlWords.nothingStaged)),
    snapshot: blocked ?? enabled,
  };
}
