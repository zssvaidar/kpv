// The stub screen: one agent endpoint, read and rendered. It is the home
// directory from GET /api/directories (#7), the first page of it, and it is
// read again each time the connection comes back. What the entry screen really
// shows is #48.

import { useEffect, useState } from 'react';

import type { DirectoryError, DirectoryListing } from '@kpv/schema';

import type { AgentClient, AgentResult } from '../api/client.ts';

type Listing = AgentResult<DirectoryListing, DirectoryError>;

export function DirectoryStub(props: {
  client: AgentClient | null;
  connected: boolean;
}) {
  const { client, connected } = props;
  const [listing, setListing] = useState<Listing | null>(null);

  useEffect(() => {
    if (client === null || !connected) {
      return;
    }
    let current = true;
    void client.listDirectories({ limit: 100 }).then((result) => {
      if (current) {
        setListing(result);
      }
    });
    return () => {
      current = false;
    };
  }, [client, connected]);

  return <DirectoryView listing={listing} />;
}

/** The rendering alone, so it can be rendered without an agent. */
export function DirectoryView(props: { listing: Listing | null }) {
  const { listing } = props;
  if (listing === null) {
    return <p className="muted">Waiting for the agent.</p>;
  }
  switch (listing.kind) {
    case 'ok':
      return <Entries listing={listing.value} />;
    case 'error':
      return (
        <p className="muted">
          The agent could not list the home directory: {listing.error.message}
        </p>
      );
    case 'rejected':
      return <p className="muted">The agent refused to list the directory.</p>;
    case 'unreachable':
      return <p className="muted">The agent did not answer.</p>;
    case 'failed':
      return (
        <p className="muted">
          The agent answered HTTP {listing.status}, which this page does not
          understand.
        </p>
      );
  }
}

function Entries(props: { listing: DirectoryListing }) {
  const { listing } = props;
  return (
    <div className="directory">
      <p className="directory-path" title={listing.path}>
        {listing.path}
        {listing.looksLikeProject ? ' (looks like a project)' : ''}
      </p>
      {listing.entries.length === 0 ? (
        <p className="muted">Empty.</p>
      ) : (
        <ul className="directory-entries">
          {listing.entries.map((entry) => (
            <li key={entry.path} data-kind={entry.kind}>
              {entry.kind === 'directory' ? `${entry.name}/` : entry.name}
            </li>
          ))}
        </ul>
      )}
      {listing.truncated ? (
        <p className="muted">
          Showing the first {listing.limit} entries the agent read.
        </p>
      ) : null}
    </div>
  );
}
