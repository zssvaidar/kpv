// The directory picker (#64): browse to a directory, see whether it looks like
// a project and why, authorize it as a project root, and see and revoke the
// roots already authorized. It replaces #8's DirectoryStub in the side panel.
//
// Everything it knows comes from the agent through the typed client, by way
// of the controller (controller.ts); this file only renders the controller's
// state and hands it the user's actions. Every name and path the agent sends
// is rendered as React text, so none of it is ever read as markup.

import {
  useEffect,
  useId,
  useRef,
  useState,
  useSyncExternalStore,
  type KeyboardEvent,
  type RefObject,
} from 'react';

import type {
  DirectoryEntry,
  DirectoryListing,
  ProjectRoot,
} from '@kpv/schema';

import type { AgentClient } from '../api/client.ts';
import { describeRequestProblem } from '../connection/describe.ts';
import type { ConnectionState } from '../connection/monitor.ts';
import {
  canChangeRoots,
  createPickerController,
  isLastKnown,
  listingGone,
  rootAt,
  type PickerController,
  type PickerState,
  type RootsAction,
  type RootsOutcome,
} from './controller.ts';
import { listKeyAction, rovingIndex } from './keyboard.ts';
import {
  directoryErrorWords,
  entryFacts,
  listingGoneNote,
  pickerProblemScope,
  revokeNote,
  rootsErrorWords,
  rootsOutcomeSentence,
  staleListingNote,
  staleNote,
  truncationNote,
  unavailableWords,
} from './words.ts';

/** What the view can ask of the picker: the controller's actions. */
export type PickerActions = Pick<
  PickerController,
  'open' | 'home' | 'refresh' | 'authorize' | 'revoke'
>;

export function DirectoryPicker(props: {
  client: AgentClient | null;
  connection: ConnectionState;
}) {
  const { client, connection } = props;
  if (client === null) {
    return (
      <p className="muted">
        This page has no session with the agent, so nothing can be browsed.
      </p>
    );
  }
  return <ConnectedPicker client={client} connection={connection} />;
}

function ConnectedPicker(props: {
  client: AgentClient;
  connection: ConnectionState;
}) {
  const { client, connection } = props;
  const [controller] = useState(() => createPickerController(client));
  const state = useSyncExternalStore(
    controller.subscribe,
    controller.current,
    controller.current,
  );
  const online = connection.status === 'connected';
  useEffect(() => {
    controller.setOnline(online);
  }, [controller, online]);
  return <PickerView state={state} actions={controller} />;
}

/** The rendering alone, so it can be rendered without an agent. */
export function PickerView(props: {
  state: PickerState;
  actions: PickerActions;
}) {
  const { state, actions } = props;
  const lastKnown = isLastKnown(state);
  const headingRef = useRef<HTMLHeadingElement>(null);

  // A directory the user opened has arrived: focus its location, so focus is
  // never left on a button that is gone.
  useEffect(() => {
    if (state.focusTick > 0) {
      headingRef.current?.focus();
    }
  }, [state.focusTick]);

  const pathFieldRef = useRef<HTMLInputElement>(null);

  return (
    <section className="picker" aria-label="Directory picker">
      <p className="picker-live" role="status" aria-live="polite">
        {state.announcement}
      </p>
      <ProblemNotice state={state} actions={actions} />
      {state.online || state.listing !== null || state.roots !== null ? null : (
        <p className="muted">Waiting for the agent.</p>
      )}
      {lastKnown && (state.listing !== null || state.roots !== null) ? (
        <p
          className="picker-stale"
          data-stale="true"
          data-cause={
            !state.online || state.problem === null
              ? 'disconnected'
              : state.problem.kind
          }
        >
          {staleNote(state.online, state.problem)}
        </p>
      ) : null}
      <Location
        state={state}
        actions={actions}
        headingRef={headingRef}
        pathFieldRef={pathFieldRef}
        lastKnown={lastKnown}
      />
      <Roots state={state} actions={actions} lastKnown={lastKnown} />
    </section>
  );
}

function ProblemNotice(props: { state: PickerState; actions: PickerActions }) {
  const { state, actions } = props;
  if (state.problem === null) {
    return null;
  }
  const description = describeRequestProblem(state.problem);
  return (
    <div
      className="connection-notice picker-problem"
      data-tone={description.tone}
      data-state={
        state.problem.kind === 'rejected'
          ? 'unauthenticated'
          : state.problem.kind
      }
    >
      <h3>The picker’s last request: {description.label}</h3>
      <p>{description.detail}</p>
      <p>{pickerProblemScope}</p>
      {state.problem.kind === 'rejected' ? null : (
        <button type="button" onClick={() => actions.refresh()}>
          Try again
        </button>
      )}
    </div>
  );
}

function Location(props: {
  state: PickerState;
  actions: PickerActions;
  headingRef: RefObject<HTMLHeadingElement | null>;
  pathFieldRef: RefObject<HTMLInputElement | null>;
  lastKnown: boolean;
}) {
  const { state, actions, headingRef, pathFieldRef, lastKnown } = props;
  const { listing } = state;
  const headingId = useId();
  return (
    <div className="picker-location">
      <h3 id={headingId} ref={headingRef} tabIndex={-1}>
        {listing === null ? 'No directory listed' : 'Current directory'}
        {lastKnown && listing !== null ? ' (last known)' : ''}
      </h3>
      {listing === null ? null : (
        <p className="picker-path" data-path="current">
          {listing.path}
        </p>
      )}
      {state.browseError?.path === null && listing === null ? (
        <p>
          The agent could not list the home directory. An authorized project
          directory below can still be opened.
        </p>
      ) : null}
      <div className="picker-nav" role="group" aria-label="Go to">
        {listing?.parent === undefined ? null : (
          <button
            type="button"
            onClick={() => {
              if (listing.parent !== undefined) {
                actions.open(listing.parent);
              }
            }}
          >
            Up to parent
          </button>
        )}
        <button type="button" onClick={() => actions.home()}>
          Home directory
        </button>
      </div>
      <PathField actions={actions} fieldRef={pathFieldRef} />
      {state.browsing === null ? null : (
        <p className="muted">
          Listing {state.browsing.path ?? 'the home directory'}…
        </p>
      )}
      <BrowseError state={state} />
      {listing === null ? null : (
        <>
          <Evidence listing={listing} />
          <Authorize state={state} actions={actions} listing={listing} />
          <Entries
            listing={listing}
            actions={actions}
            headingId={headingId}
            pathFieldRef={pathFieldRef}
          />
        </>
      )}
    </div>
  );
}

function PathField(props: {
  actions: PickerActions;
  fieldRef: RefObject<HTMLInputElement | null>;
}) {
  const { actions, fieldRef } = props;
  const [typed, setTyped] = useState('');
  const id = useId();
  return (
    <form
      className="picker-path-form"
      onSubmit={(event) => {
        event.preventDefault();
        if (typed !== '') {
          actions.open(typed);
        }
      }}
    >
      <label htmlFor={id}>Go to an absolute path</label>
      <div>
        <input
          id={id}
          ref={fieldRef}
          type="text"
          spellCheck={false}
          autoComplete="off"
          value={typed}
          onChange={(event) => setTyped(event.target.value)}
        />
        <button type="submit">Go</button>
      </div>
    </form>
  );
}

function BrowseError(props: { state: PickerState }) {
  const failure = props.state.browseError;
  if (failure === null) {
    return null;
  }
  const words = directoryErrorWords[failure.error.error];
  return (
    <div className="picker-error" data-error={failure.error.error}>
      <h4>
        {failure.error.error === 'forbidden' ? 'Refused: ' : 'Could not list: '}
        {words.title}
      </h4>
      <p>
        {failure.path === null ? 'The home directory' : failure.path}:{' '}
        {words.detail}
      </p>
      <p>
        The agent said: <q>{failure.error.message}</q>
      </p>
      {props.state.listing === null ? null : listingGone(props.state) ? (
        <p className="muted" data-still-showing="stale">
          {staleListingNote(props.state.listing.path)}
        </p>
      ) : (
        <p className="muted" data-still-showing="current">
          Still showing {props.state.listing.path}.
        </p>
      )}
    </div>
  );
}

function Evidence(props: { listing: DirectoryListing }) {
  const { listing } = props;
  if (!listing.looksLikeProject) {
    return (
      <p className="picker-evidence" data-project="false">
        No project markers here. It can still be authorized: the markers are a
        hint, not a requirement.
      </p>
    );
  }
  return (
    <div className="picker-evidence" data-project="true">
      <p>Looks like a project. Markers found here:</p>
      <ul>
        {listing.evidence.map((item) => (
          <li key={item.marker}>
            <code>{item.marker}</code>: {item.describes}
          </li>
        ))}
      </ul>
    </div>
  );
}

function Authorize(props: {
  state: PickerState;
  actions: PickerActions;
  listing: DirectoryListing;
}) {
  const { state, actions, listing } = props;
  const existing = rootAt(state, listing.path);
  const already = existing?.available === true;
  const inFlight = state.busy?.action === 'authorize';
  // The listed directory could not be listed again: the agent would refuse it.
  const gone = listingGone(state) ? state.listingFailure : null;
  const enabled = !already && gone === null && canChangeRoots(state);
  const goneId = useId();
  let label = 'Authorize this directory';
  if (already) {
    label = 'Already authorized';
  } else if (inFlight) {
    label = 'Authorizing…';
  }
  return (
    <div className="picker-authorize">
      <button
        type="button"
        disabled={!enabled}
        aria-describedby={gone === null ? undefined : goneId}
        onClick={() => actions.authorize()}
      >
        {label}
      </button>
      {gone === null ? null : (
        <p id={goneId} className="picker-gone" data-gone={gone.error.error}>
          {listingGoneNote(listing.path, gone.error.error)}
        </p>
      )}
      <AuthorizeOutcome state={state} actions={actions} />
    </div>
  );
}

function AuthorizeOutcome(props: {
  state: PickerState;
  actions: PickerActions;
}) {
  const { state, actions } = props;
  const outcome = state.outcome;
  if (outcome === null) {
    return null;
  }
  if (outcome.kind === 'authorized') {
    return (
      <p className="picker-outcome" data-outcome="authorized">
        Authorized: <span className="picker-path">{outcome.path}</span>
      </p>
    );
  }
  if (isSentenceOutcome(outcome, 'authorize')) {
    return <SentenceOutcome outcome={outcome} />;
  }
  if (outcome.kind !== 'refused' || outcome.action !== 'authorize') {
    return null;
  }
  const words = rootsErrorWords[outcome.error.error];
  const overlapped = outcome.error.root;
  return (
    <div
      className="picker-error"
      data-outcome="refused"
      data-error={outcome.error.error}
    >
      <h4>Not authorized: {words.title}</h4>
      <p>{words.detail}</p>
      <p>
        The agent said: <q>{outcome.error.message}</q>
      </p>
      {outcome.error.error === 'overlaps' && overlapped !== undefined ? (
        <div className="picker-overlap">
          <p>
            Already authorized:{' '}
            <span className="picker-path">{overlapped}</span>
          </p>
          <button type="button" onClick={() => actions.open(overlapped)}>
            Open {overlapped}
          </button>
          <button
            type="button"
            disabled={!canChangeRoots(state)}
            onClick={() => actions.revoke(overlapped)}
          >
            Revoke {overlapped} (deletes nothing on disk)
          </button>
        </div>
      ) : null}
    </div>
  );
}

type SentenceOutcomeKind = Extract<
  RootsOutcome,
  { kind: 'rejected' | 'unconfirmed' | 'settled' }
>;

/** An outcome said in one sentence, the one the live region also reads. */
function isSentenceOutcome(
  outcome: RootsOutcome,
  action: RootsAction,
): outcome is SentenceOutcomeKind {
  return (
    (outcome.kind === 'rejected' ||
      outcome.kind === 'unconfirmed' ||
      outcome.kind === 'settled') &&
    outcome.action === action
  );
}

function SentenceOutcome(props: { outcome: SentenceOutcomeKind }) {
  const { outcome } = props;
  return (
    <p
      className="picker-outcome"
      data-outcome={outcome.kind}
      data-reason={
        outcome.kind === 'unconfirmed' ? outcome.reason.kind : undefined
      }
      data-listed={outcome.kind === 'settled' ? outcome.listed : undefined}
    >
      {rootsOutcomeSentence(outcome)}
    </p>
  );
}

function Entries(props: {
  listing: DirectoryListing;
  actions: PickerActions;
  headingId: string;
  pathFieldRef: RefObject<HTMLInputElement | null>;
}) {
  const { listing, actions, headingId, pathFieldRef } = props;
  const [active, setActive] = useState(0);
  const buttons = useRef<(HTMLButtonElement | null)[]>([]);
  const count = listing.entries.length;
  // A new directory starts at its first entry.
  useEffect(() => {
    setActive(0);
  }, [listing.path]);
  const tabStop = rovingIndex(active, count);

  function onKeyDown(event: KeyboardEvent<HTMLButtonElement>, index: number) {
    const action = listKeyAction(
      event,
      index,
      count,
      listing.parent !== undefined,
    );
    if (action === null) {
      return;
    }
    event.preventDefault();
    switch (action.kind) {
      case 'focus':
        setActive(action.index);
        buttons.current[action.index]?.focus();
        return;
      case 'parent':
        if (listing.parent !== undefined) {
          actions.open(listing.parent);
        }
        return;
      case 'pathField':
        pathFieldRef.current?.focus();
        return;
    }
  }

  return (
    <div className="picker-entries">
      {listing.truncated ? (
        <p className="picker-truncated" data-truncated="true">
          {truncationNote(count)}
        </p>
      ) : null}
      {count === 0 ? (
        <p className="muted">This directory is empty.</p>
      ) : (
        <ul aria-labelledby={headingId} aria-describedby={`${headingId}-keys`}>
          {listing.entries.map((entry, index) => (
            <li key={entry.path}>
              <EntryButton
                entry={entry}
                tabIndex={index === tabStop ? 0 : -1}
                buttonRef={(element) => {
                  buttons.current[index] = element;
                }}
                onFocus={() => setActive(index)}
                onKeyDown={(event) => onKeyDown(event, index)}
                onOpen={() => actions.open(entry.path)}
              />
            </li>
          ))}
        </ul>
      )}
      <p id={`${headingId}-keys`} className="muted picker-keys">
        Arrow keys move between entries, Enter opens one, Backspace goes up to
        the parent, / goes to the path field.
      </p>
    </div>
  );
}

function EntryButton(props: {
  entry: DirectoryEntry;
  tabIndex: number;
  buttonRef: (element: HTMLButtonElement | null) => void;
  onFocus: () => void;
  onKeyDown: (event: KeyboardEvent<HTMLButtonElement>) => void;
  onOpen: () => void;
}) {
  const { entry, tabIndex, buttonRef, onFocus, onKeyDown, onOpen } = props;
  const facts = entryFacts(entry);
  // An entry that cannot be opened stays focusable (aria-disabled, not
  // disabled), so it is reached and its reason is read out with it.
  return (
    <button
      type="button"
      className="picker-entry"
      data-kind={entry.kind}
      data-openable={facts.openable}
      aria-disabled={!facts.openable}
      tabIndex={tabIndex}
      ref={buttonRef}
      onFocus={onFocus}
      onKeyDown={onKeyDown}
      onClick={() => {
        if (facts.openable) {
          onOpen();
        }
      }}
    >
      <span className="picker-entry-name">{entry.name}</span>
      <span className="picker-entry-kind">
        {facts.kind}
        {entry.unreadable ? ', unreadable' : ''}
        {facts.reason === null ? '' : ` (cannot be opened: ${facts.reason})`}
      </span>
    </button>
  );
}

function Roots(props: {
  state: PickerState;
  actions: PickerActions;
  lastKnown: boolean;
}) {
  const { state, actions, lastKnown } = props;
  const headingId = useId();
  const outcome = state.outcome;
  return (
    <div className="picker-roots">
      <h3 id={headingId}>
        Authorized project directories
        {lastKnown && state.roots !== null ? ' (last known)' : ''}
      </h3>
      <p className="muted">{revokeNote}</p>
      {state.rootsError === null ? null : (
        <p className="picker-error" data-error={state.rootsError.error}>
          The list could not be read:{' '}
          {rootsErrorWords[state.rootsError.error].title}. The agent said:{' '}
          <q>{state.rootsError.message}</q>
        </p>
      )}
      {outcome?.kind === 'revoked' ? (
        <p className="picker-outcome" data-outcome="revoked">
          Revoked <span className="picker-path">{outcome.path}</span>. Nothing
          on disk was deleted.
        </p>
      ) : null}
      {outcome !== null && isSentenceOutcome(outcome, 'revoke') ? (
        <SentenceOutcome outcome={outcome} />
      ) : null}
      {outcome?.kind === 'refused' && outcome.action === 'revoke' ? (
        <div
          className="picker-error"
          data-outcome="refused"
          data-error={outcome.error.error}
        >
          <h4>Not revoked: {rootsErrorWords[outcome.error.error].title}</h4>
          <p>
            {outcome.error.error === 'notFound'
              ? `${outcome.path} is not an authorized directory any more, perhaps because it was revoked in another tab. The list below is the agent's.`
              : rootsErrorWords[outcome.error.error].detail}
          </p>
          <p>
            The agent said: <q>{outcome.error.message}</q>
          </p>
        </div>
      ) : null}
      {state.roots === null ? (
        <p className="muted">Not read yet.</p>
      ) : state.roots.length === 0 ? (
        <p className="muted">None yet.</p>
      ) : (
        <ul aria-labelledby={headingId}>
          {state.roots.map((root) => (
            <RootItem
              key={root.path}
              root={root}
              state={state}
              actions={actions}
            />
          ))}
        </ul>
      )}
    </div>
  );
}

function RootItem(props: {
  root: ProjectRoot;
  state: PickerState;
  actions: PickerActions;
}) {
  const { root, state, actions } = props;
  const revoking =
    state.busy?.action === 'revoke' && state.busy.path === root.path;
  return (
    <li className="picker-root" data-available={root.available}>
      <span className="picker-path">{root.path}</span>
      <span className="picker-root-status">
        {root.available ? 'available' : unavailableWords[root.reason ?? 'io']}
      </span>
      <span className="picker-root-actions">
        {root.available ? (
          <button type="button" onClick={() => actions.open(root.path)}>
            Open <span className="visually-hidden">{root.path}</span>
          </button>
        ) : null}
        <button
          type="button"
          disabled={!canChangeRoots(state)}
          onClick={() => actions.revoke(root.path)}
        >
          {revoking ? 'Revoking…' : 'Revoke'}{' '}
          <span className="visually-hidden">{root.path}</span> (deletes nothing
          on disk)
        </button>
      </span>
    </li>
  );
}
