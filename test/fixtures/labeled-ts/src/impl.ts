// Construct 5 (part 1 of 3): the original definition. facade.ts re-exports
// this; main.ts imports the re-export and calls it. The CALLS edge must
// resolve to THIS function, not a phantom re-export node.
export function doWork(): string {
  return "done";
}
