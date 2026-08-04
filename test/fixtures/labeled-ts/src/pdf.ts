// Construct 1: interface + implementing class + caller through the
// interface type. run() calls ops.generate(...) where ops is typed as the
// PdfOps interface, but the only concrete implementation in this fixture is
// PdfGenerator — the call graph must resolve run -> PdfGenerator.generate.
export interface PdfOps {
  generate(x: string): void;
}

export class PdfGenerator implements PdfOps {
  generate(x: string): void {
    console.log(`generating ${x}`);
  }
}

export function run(ops: PdfOps): void {
  ops.generate("doc");
}
