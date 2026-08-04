// Ambient declarations for the NestJS-style decorators used in
// items-controller.ts. No real "@nestjs/common" package is installed —
// scip-typescript only needs these symbols to type-check, not the real
// framework runtime, since the decorators here are never invoked (NestJS
// wires them up via reflection at runtime, out of scope for static
// indexing).
declare function Controller(prefix?: string): ClassDecorator;
declare function Get(path?: string): MethodDecorator;
