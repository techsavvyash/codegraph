import { createLogger } from "./logger";

// RFC-010 golden cases:
//  - logOne is a top-level arrow bound via a declarator: its SCIP definition
//    occurrence sits on the VARIABLE identifier, outside the arrow node's
//    span. Without declarator widening the calls in its body would have no
//    enclosing function and the edges would be dropped.
//  - twice is a function-scoped const: SCIP emits it as a local, so it has
//    no Function node. Calls inside it must attribute to the innermost
//    graph-visible function (processAll).
//  - twice invokes logOne from two call sites; the collapsed
//    processAll->logOne edge must carry the smaller line (min-line dedup).
export const logOne = (item: string): void => {
  const logger = createLogger("nested");
  logger.log(item);
};

export function processAll(items: string[]): void {
  const twice = (item: string): void => {
    logOne(item);
    logOne(item.toUpperCase());
  };
  twice(items[0] ?? "first");
}
