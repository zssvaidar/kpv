// The x-kpv-uniqueBy annotation rule. The Go agent carries the same algorithm
// in agent/internal/pipeline/validate.go; keeping both small and identical is
// what keeps the two sides reporting the same locations.

/** One failure found by an annotation rule, located like a schema failure. */
export interface AnnotationViolation {
  /** JSON Pointer into the document; '' is the root. */
  instanceLocation: string;
  /** JSON Pointer into the schema, naming the annotation that rejected it. */
  keywordLocation: string;
  /** Human-readable explanation, not a stable identifier. */
  message: string;
}

const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);

/** Escapes one JSON Pointer reference token, per RFC 6901. */
const escapePointerToken = (token: string): string =>
  token.replace(/~/g, '~0').replace(/\//g, '~1');

/**
 * Applies every x-kpv-uniqueBy annotation reachable from the schema root
 * through `properties`. The annotation names a property of an array's items
 * that has to be unique across the array, which is the one rule in the pipeline
 * model that JSON Schema cannot state: no duplicate processing-step
 * identifiers within one pipeline.
 *
 * Recursion deliberately only follows `properties`: the annotation is a local
 * extension, and keeping its reach small keeps it readable and keeps the Go and
 * TypeScript implementations identical.
 */
export function uniqueByViolations(
  schemaNode: unknown,
  value: unknown,
  keywordLocation = '',
  instanceLocation = '',
): AnnotationViolation[] {
  if (!isRecord(schemaNode)) return [];
  const found: AnnotationViolation[] = [];

  const uniqueBy = schemaNode['x-kpv-uniqueBy'];
  if (typeof uniqueBy === 'string' && Array.isArray(value)) {
    const seen = new Map<string, number>();
    value.forEach((item, index) => {
      if (!isRecord(item)) return;
      const key = item[uniqueBy];
      if (typeof key !== 'string') return;
      const first = seen.get(key);
      if (first === undefined) {
        seen.set(key, index);
        return;
      }
      found.push({
        instanceLocation: `${instanceLocation}/${index}/${escapePointerToken(uniqueBy)}`,
        keywordLocation: `${keywordLocation}/x-kpv-uniqueBy`,
        message: `duplicate ${uniqueBy} "${key}", already used at index ${first}`,
      });
    });
  }

  const properties = schemaNode.properties;
  if (!isRecord(properties) || !isRecord(value)) return found;
  for (const name of Object.keys(properties).sort()) {
    if (!(name in value)) continue;
    const token = escapePointerToken(name);
    found.push(
      ...uniqueByViolations(
        properties[name],
        value[name],
        `${keywordLocation}/properties/${token}`,
        `${instanceLocation}/${token}`,
      ),
    );
  }
  return found;
}
