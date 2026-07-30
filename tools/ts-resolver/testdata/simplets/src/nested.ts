// Exercises a nested/namespaced container shape defensively on the Go join
// side (descriptor `file.ts`/Outer#Inner#method(). — see
// tsresolver_test.go). This class itself has no relationship to Store; it
// only needs to compile so the Program includes it in the fixture.
export class Outer {
  inner = {
    helper(): void {
      /* no-op */
    },
  };
}
