// toplevel.ts exercises RFC-013 follow-up classification (tasks #18/#19).
// The original labeled-ts design discovered that top-level calls produced
// ZERO CALLS edges (see labeled-ts.json construct 5's comment); these
// constructs assert the fixed behavior directly.
import { registerThing, valueOnly, bodyValue } from './registry';

// Module-scope CALL: runs at import time. Expected:
// (File toplevel.ts)-[:CALLS]->(Function registerThing).
export const registration = registerThing();

// Module-scope VALUE reference: no invocation. Expected: no CALLS edge to
// valueOnly from anywhere.
export const savedRef = valueOnly;

// In-body VALUE reference. Expected: no CALLS edge from useBodyValue to
// bodyValue.
export function useBodyValue(): () => void {
  const h = bodyValue;
  return h;
}
