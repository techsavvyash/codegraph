// Construct 5 (part 3 of 3): caller imports doWork through the re-export
// facade, not directly from impl.ts. callDoWork wraps the call in a named
// function — the call-graph builder attributes CALLS edges to an enclosing
// Function/Method node, and a bare top-level statement has none, so the
// edge would be silently dropped for reasons unrelated to re-export
// resolution if left at module scope.
import { doWork } from "./facade";
import { PdfGenerator, run } from "./pdf";
import { Paginator } from "./options";
import { ItemsController } from "./items-controller";
import { greetUser } from "./greet";

export function callDoWork(): string {
  return doWork();
}

console.log(callDoWork());
run(new PdfGenerator());
new Paginator();
new ItemsController();
console.log(greetUser("world"));
