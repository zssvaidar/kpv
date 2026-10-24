import { schemaPackageName } from '@kpv/schema';

/** Proves the workspace is wired up end to end. No web feature lives here yet. */
export function describeWorkspace(): string {
  return `kpv web is wired to ${schemaPackageName}`;
}
