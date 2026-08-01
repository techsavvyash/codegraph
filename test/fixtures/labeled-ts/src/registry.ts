// registry.ts: call targets for toplevel.ts's classification constructs
// (module-scope call vs value reference) — see toplevel.ts.
export function registerThing(): number {
  return 1;
}

export function valueOnly(): void {}

export function bodyValue(): void {}
