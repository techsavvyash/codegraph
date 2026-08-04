// Construct 4: const-bound arrow function promoted to Function, called by a
// named function — analogous to tiny-ts/src/nested.ts's logOne/processAll
// pair but a distinct, separately-labeled case. formatName's SCIP
// definition occurrence sits on the VARIABLE identifier outside the arrow
// node's span; without declarator widening (kind_promotion.go) it would be
// classified Variable, not Function, and invisible to the call graph.
export const formatName = (n: string): string => n.trim();

export function greetUser(n: string): string {
  return "Hi " + formatName(n);
}
