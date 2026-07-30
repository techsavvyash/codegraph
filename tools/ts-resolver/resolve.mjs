#!/usr/bin/env node
// resolve.mjs — RFC-001 Layer 3 TypeScript structural type resolver.
//
// scip-typescript only emits `is_implementation` relationships for classes
// that explicitly declare `implements`/`extends` (FileIndexer.ts's
// forEachAncestor walk) plus a handful of object-literal contextual-typing
// cases. It never runs a general structural-assignability check, so a class
// that satisfies a local interface's shape WITHOUT an `implements` clause
// gets no relationship at all — calls through the interface fan out to
// nothing, and the concrete methods look dead.
//
// This script fills that gap using the TypeScript compiler's own type
// checker (the only thing authoritative for TypeScript's structural typing
// rules): it loads the target project's tsconfig, enumerates every
// non-empty interface and every class, and for each (class, interface) pair
// asks `checker.isTypeAssignableTo(classType, ifaceType)` — the same public
// API (stable since TS 5.4) that a `satisfies`/structural check would use.
// Matches are recorded at both the type level and, for every interface
// member, the method level (so the Go side can join method-to-method, not
// just class-to-interface).
//
// Output is a single JSON document on --out; see the module-level jsdoc
// typedefs below for the exact shape. This process never talks to Neo4j or
// SCIP directly — internal/ingest/resolve/tsresolver.go joins this output
// onto real scip-typescript symbol strings.
//
// Usage:
//   node resolve.mjs --project <absoluteProjectRoot> --out <jsonPath> [--ts-module <path>]
//
// Exit codes:
//   0  success (including the capped/degenerate "wrote empty output" case)
//   1  usage error (missing/bad flags)
//   2  `typescript` module not found in the target project's node_modules
//      (and no --ts-module override resolved either)
//   3  the resolved `typescript` install is too old: isTypeAssignableTo is
//      not present (public since TS 5.4 — see microsoft/TypeScript#56448)
//   4  unexpected internal failure (tsconfig not found, Program creation
//      failed, etc.) — printed to stderr with a stack trace

import { createRequire } from "node:module";
import { readFileSync, writeFileSync } from "node:fs";
import path from "node:path";

/**
 * @typedef {Object} TSRelationship
 * @property {string} fromFile   POSIX-relative path (to project root) of the class's declaring file
 * @property {string} fromType   class name
 * @property {string} fromMethod method name on the class side, or "" for a type-level relationship
 * @property {string} toFile     POSIX-relative path (to project root) of the interface's declaring file
 * @property {string} toType     interface name
 * @property {string} toMethod   member name on the interface side, or "" for a type-level relationship
 */

function parseArgs(argv) {
  const args = { project: null, out: null, tsModule: null };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === "--project") {
      args.project = argv[++i];
    } else if (a === "--out") {
      args.out = argv[++i];
    } else if (a === "--ts-module") {
      args.tsModule = argv[++i];
    } else {
      throw new UsageError(`unrecognized argument: ${a}`);
    }
  }
  if (!args.project) throw new UsageError("--project <absoluteProjectRoot> is required");
  if (!args.out) throw new UsageError("--out <jsonPath> is required");
  return args;
}

class UsageError extends Error {}

/**
 * Loads the `typescript` module. Resolution order:
 *   1. --ts-module <path>, if given (a path to typescript's entry module or
 *      its package directory) — exists purely so tests can point this
 *      script at a `typescript` install that lives outside the fixture
 *      project (fixtures deliberately do not vendor their own copy of
 *      typescript; see tools/ts-resolver/testdata/simplets/package.json).
 *   2. `typescript` resolved from <projectRoot>/package.json, i.e. the
 *      TARGET project's own node_modules — this is what real indexing runs
 *      against, so the type-checking behavior matches exactly what that
 *      project would get from its own `tsc`.
 *
 * Throws a TSModuleNotFoundError (exit 2) if neither resolves.
 */
function loadTypeScript(projectRoot, tsModuleOverride) {
  if (tsModuleOverride) {
    const req = createRequire(path.join(process.cwd(), "noop.js"));
    try {
      return req(path.resolve(tsModuleOverride));
    } catch (e) {
      throw new TSModuleNotFoundError(
        `--ts-module ${tsModuleOverride} could not be loaded: ${e.message}`,
      );
    }
  }

  const pkgJsonPath = path.join(projectRoot, "package.json");
  let req;
  try {
    req = createRequire(pkgJsonPath);
  } catch (e) {
    throw new TSModuleNotFoundError(
      `could not create a module resolver rooted at ${pkgJsonPath}: ${e.message}`,
    );
  }
  try {
    return req("typescript");
  } catch (e) {
    throw new TSModuleNotFoundError(
      `'typescript' is not resolvable from ${projectRoot} (checked its node_modules via ${pkgJsonPath}): ${e.message}`,
    );
  }
}

class TSModuleNotFoundError extends Error {}
class TSVersionTooOldError extends Error {}

function assertTypeScriptSupportsIsTypeAssignableTo(ts) {
  const version = ts.version || "unknown";
  const major = parseInt(version.split(".")[0], 10);
  const minor = parseInt(version.split(".")[1], 10);
  const versionOk =
    !Number.isNaN(major) && (major > 5 || (major === 5 && minor >= 4));
  if (!versionOk) {
    throw new TSVersionTooOldError(
      `typescript ${version} is too old: isTypeAssignableTo requires TypeScript >= 5.4`,
    );
  }
  // Belt-and-suspenders: the version string check above is the primary
  // signal, but if some build reports a satisfying version string yet
  // doesn't actually expose the API (e.g. a patched/stripped build), catch
  // that too once we have a checker instance (see main()).
}

/** POSIX-ify a path.relative result — scip-typescript descriptors always use forward slashes. */
function toPosixRelative(fromDir, absPath) {
  const rel = path.relative(fromDir, absPath);
  return rel.split(path.sep).join("/");
}

function isNonDeclarationProjectFile(ts, fileName, projectRootAbs) {
  if (fileName.endsWith(".d.ts")) return false;
  const rel = path.relative(projectRootAbs, fileName);
  if (rel.startsWith("..") || path.isAbsolute(rel)) return false; // outside project root (lib.*.d.ts etc.)
  if (rel.split(path.sep).includes("node_modules")) return false;
  return true;
}

function hasAtLeastOneMember(ts, node) {
  return (
    node.members &&
    node.members.some(
      (m) => ts.isMethodSignature(m) || ts.isPropertySignature(m),
    )
  );
}

// An interface qualifies for structural matching only when it has at least
// one REQUIRED function-typed member (a non-optional method signature, or a
// non-optional property whose declared type is a function type). Rationale:
// this resolver exists for call resolution (RFC-001) — an interface with no
// required callables is a data shape, and an all-optional interface is
// universally assignable (every class in the project "implements"
// PaginationQuery-style option bags), which floods the graph with
// meaningless IMPLEMENTS edges. Measured on a real NestJS backend, ~96% of
// unfiltered type-level matches were exactly this noise.
function hasRequiredCallableMember(ts, checker, node) {
  if (!node.members) return false;
  return node.members.some((m) => {
    if (m.questionToken) return false;
    if (ts.isMethodSignature(m)) return true;
    if (ts.isPropertySignature(m) && m.type) {
      if (ts.isFunctionTypeNode(m.type)) return true;
      // Property typed via alias/reference that resolves to something
      // callable (e.g. `save: SaveFn`): ask the checker.
      const t = checker.getTypeAtLocation(m.type);
      return t.getCallSignatures().length > 0;
    }
    return false;
  });
}

function main() {
  const args = parseArgs(process.argv.slice(2));
  const projectRootAbs = path.resolve(args.project);

  const ts = loadTypeScript(projectRootAbs, args.tsModule);
  assertTypeScriptSupportsIsTypeAssignableTo(ts);

  const configPath = ts.findConfigFile(
    projectRootAbs,
    ts.sys.fileExists,
    "tsconfig.json",
  );
  if (!configPath) {
    throw new Error(`no tsconfig.json found under ${projectRootAbs}`);
  }

  const configFile = ts.readConfigFile(configPath, ts.sys.readFile);
  if (configFile.error) {
    throw new Error(
      `failed to read ${configPath}: ${ts.flattenDiagnosticMessageText(configFile.error.messageText, "\n")}`,
    );
  }

  const parsed = ts.parseJsonConfigFileContent(
    configFile.config,
    ts.sys,
    path.dirname(configPath),
  );

  const program = ts.createProgram({
    rootNames: parsed.fileNames,
    options: parsed.options,
  });
  const checker = program.getTypeChecker();

  if (typeof checker.isTypeAssignableTo !== "function") {
    throw new TSVersionTooOldError(
      `typescript ${ts.version} does not expose checker.isTypeAssignableTo (requires TypeScript >= 5.4)`,
    );
  }

  /** @type {{node: import('typescript').InterfaceDeclaration, file: string, name: string}[]} */
  const interfaces = [];
  /** @type {{node: import('typescript').ClassDeclaration, file: string, name: string}[]} */
  const classes = [];
  let skippedEmptyInterfaces = 0;
  let skippedNoRequiredCallable = 0;

  for (const sourceFile of program.getSourceFiles()) {
    if (!isNonDeclarationProjectFile(ts, sourceFile.fileName, projectRootAbs)) {
      continue;
    }
    const relFile = toPosixRelative(projectRootAbs, sourceFile.fileName);

    const visit = (node) => {
      if (ts.isInterfaceDeclaration(node) && node.name) {
        if (!hasAtLeastOneMember(ts, node)) {
          skippedEmptyInterfaces++;
        } else if (!hasRequiredCallableMember(ts, checker, node)) {
          skippedNoRequiredCallable++;
        } else {
          interfaces.push({ node, file: relFile, name: node.name.text });
        }
      } else if (ts.isClassDeclaration(node) && node.name) {
        classes.push({ node, file: relFile, name: node.name.text });
      }
      ts.forEachChild(node, visit);
    };
    visit(sourceFile);
  }

  const stats = {
    interfaces: interfaces.length,
    classes: classes.length,
    pairsChecked: 0,
    typeLevel: 0,
    methodLevel: 0,
    skippedEmptyInterfaces,
    skippedNoRequiredCallable,
  };

  const pairCount = interfaces.length * classes.length;
  const CAP = 2_000_000;
  if (pairCount > CAP) {
    writeFileSync(
      args.out,
      JSON.stringify(
        {
          resolver: "ts-structural",
          tsVersion: ts.version,
          relationships: [],
          stats: { ...stats, capExceeded: true },
        },
        null,
        2,
      ),
    );
    return;
  }

  // Resolve all types once up front (checker.getTypeAtLocation is not free),
  // then run the O(I×C) assignability loop over cached Type objects.
  const ifaceTypes = interfaces.map((i) => ({
    ...i,
    type: checker.getTypeAtLocation(i.node.name),
  }));
  const classTypes = classes.map((c) => ({
    ...c,
    type: checker.getTypeAtLocation(c.node.name),
  }));

  /** @type {TSRelationship[]} */
  const relationships = [];

  for (const cls of classTypes) {
    for (const iface of ifaceTypes) {
      stats.pairsChecked++;

      // Direction matters: the CLASS type must be assignable TO the
      // INTERFACE type (the class satisfies the interface's shape), not
      // the reverse.
      if (!checker.isTypeAssignableTo(cls.type, iface.type)) continue;

      relationships.push({
        fromFile: cls.file,
        fromType: cls.name,
        fromMethod: "",
        toFile: iface.file,
        toType: iface.name,
        toMethod: "",
      });
      stats.typeLevel++;

      for (const member of iface.node.members) {
        if (!ts.isMethodSignature(member) && !ts.isPropertySignature(member)) {
          continue;
        }
        if (!member.name || !ts.isIdentifier(member.name)) continue;
        const memberName = member.name.text;

        const classProp = checker.getPropertyOfType(cls.type, memberName);
        if (!classProp || !classProp.declarations || classProp.declarations.length === 0) {
          continue; // resolves to nothing concrete on the class side — not joinable
        }

        relationships.push({
          fromFile: cls.file,
          fromType: cls.name,
          fromMethod: memberName,
          toFile: iface.file,
          toType: iface.name,
          toMethod: memberName,
        });
        stats.methodLevel++;
      }
    }
  }

  writeFileSync(
    args.out,
    JSON.stringify(
      {
        resolver: "ts-structural",
        tsVersion: ts.version,
        relationships,
        stats,
      },
      null,
      2,
    ),
  );
}

try {
  main();
} catch (e) {
  if (e instanceof UsageError) {
    process.stderr.write(`usage error: ${e.message}\n`);
    process.exit(1);
  }
  if (e instanceof TSModuleNotFoundError) {
    process.stderr.write(`typescript module not found: ${e.message}\n`);
    process.exit(2);
  }
  if (e instanceof TSVersionTooOldError) {
    process.stderr.write(`typescript version too old: ${e.message}\n`);
    process.exit(3);
  }
  process.stderr.write(`ts-resolver failed: ${e.stack || e.message}\n`);
  process.exit(4);
}
