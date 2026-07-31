export function square(n: number): number {
  return n * n;
}

export function sumOfSquares(a: number, b: number): number {
  // Top-level function calling another top-level function.
  return square(a) + square(b);
}

// const-bound arrow calling a top-level function.
export const doubleSquare = (n: number): number => {
  return square(n) * 2;
};

export function usesAnonymousCallback(values: number[]): number[] {
  // Anonymous callback: not a named enclosure, must be skipped by the
  // oracle even though it contains a resolvable call to square().
  return values.map((v) => square(v));
}

export function usesExternalCall(raw: string): unknown {
  // External call (into a lib.d.ts / node_modules declaration): must be
  // skipped as skippedExternal.
  return JSON.parse(raw);
}
