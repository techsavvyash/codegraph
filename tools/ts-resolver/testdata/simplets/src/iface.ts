// Store is a two-method interface. GoodStore (no `implements` clause) and
// ExplicitStore (has one) both satisfy it structurally; BadStore and
// WrongSig do not.
export interface Store {
  save(s: string): Promise<void>;
  load(): Promise<string>;
}

// Marker is an empty interface: universally satisfied by everything, so the
// resolver must skip it entirely rather than emitting a relationship for
// every class in the fixture.
export interface Marker {}

// Options is an all-optional interface: EVERY class in the project is
// assignable to it, so structural matching against it is pure noise
// (measured ~96% of matches on a real NestJS backend were shapes like
// this). The resolver must skip it: no required callable member.
export interface Options {
  page?: number;
  limit?: number;
  onDone?: () => void;
}

// Payment is a required-members data shape with no function-typed members.
// Structurally satisfying a data shape says nothing about call resolution,
// so the resolver must skip it too.
export interface Payment {
  amount: number;
  currency: string;
}
