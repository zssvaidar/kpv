// `pnpm dev`: build and start a fresh agent, start the Vite dev server in
// front of it, and print the one URL to open.
//
// Every run starts its own agent, so every run learns that agent's port and
// token afresh: nothing is configured, nothing is committed, and running it
// twice in a row works with the agent on a different port each time.

import { startDev } from './dev-server.ts';

const setup = await startDev();

for (const line of setup.agent.banner) {
  process.stdout.write(`${line}\n`);
}
process.stdout.write(
  `agent at ${setup.agent.origin}, proxied as ${setup.origin}/api\n` +
    `open ${setup.openUrl}\n`,
);

let stopping = false;
const stop = (code: number) => {
  if (stopping) {
    return;
  }
  stopping = true;
  void setup.stop().finally(() => process.exit(code));
};
process.on('SIGINT', () => stop(0));
process.on('SIGTERM', () => stop(0));
// The dev server outlives the agent on purpose: the page then shows what an
// agent that went away looks like. A new agent is a new port and a new token,
// so the way on is to run this again and open the URL it prints.
void setup.agent.exited.then((code) => {
  if (!stopping) {
    process.stderr.write(
      `the agent exited (${code}); the page now shows it as not reachable. ` +
        'Stop this and run pnpm dev again for a new agent.\n',
    );
  }
});
