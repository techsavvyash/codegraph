import { Store } from "./iface";

// GoodStore structurally satisfies Store but never declares `implements` —
// this is the exact gap RFC-001 targets: scip-typescript emits no
// is_implementation relationship for this class at all.
export class GoodStore {
  async save(s: string): Promise<void> {
    void s;
  }
  async load(): Promise<string> {
    return "";
  }
}

// ExplicitStore also satisfies Store, AND declares `implements` — included
// so the resolver's "don't filter out explicit-heritage pairs" behavior is
// exercised (the Go side dedupes against SCIP-native relationships, not
// this script).
export class ExplicitStore implements Store {
  async save(s: string): Promise<void> {
    void s;
  }
  async load(): Promise<string> {
    return "";
  }
}

// BadStore only implements half of Store's method set — must NOT be
// reported as implementing Store.
export class BadStore {
  async save(s: string): Promise<void> {
    void s;
  }
}

// WrongSig has a same-named method with an incompatible signature (number
// instead of string) — must NOT be reported as implementing Store.
export class WrongSig {
  async save(s: number): Promise<void> {
    void s;
  }
  async load(): Promise<string> {
    return "";
  }
}
