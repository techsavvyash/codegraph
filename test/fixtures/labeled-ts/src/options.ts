// Construct 2: the 767-noise regression class. Options has ONLY optional
// members (page?, onDone?) — per tsresolver.go's SkippedNoRequiredCallable
// rule, an interface with no required function-typed member is universally
// assignable and must be skipped entirely by the structural resolver, never
// producing an IMPLEMENTS edge. Paginator structurally matches Options'
// shape (same optional properties) but does NOT `implements Options` and
// must never be linked to it by any detection path — this is the single
// most important negative assertion in this fixture set.
export interface Options {
  page?: number;
  onDone?: () => void;
}

export class Paginator {
  page?: number;
  onDone?: () => void;

  other(): void {
    // deliberately unrelated to Options
  }
}
