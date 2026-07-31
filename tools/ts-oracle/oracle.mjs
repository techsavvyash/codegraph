#!/usr/bin/env node
// oracle.mjs — RFC-013 Layer 2 TypeScript sampled differential call-site oracle.
//
// Loads the TARGET project's own TypeScript compiler (same strategy as
// tools/ts-resolver/resolve.mjs — never bundle a copy of `typescript`;
// always resolve from the project being checked so results match what that
// project's own `tsc` would say) and enumerates every CallExpression across
// the program's project source files. For each call site it resolves the
// callee declaration via the compiler's own binder/checker
// (checker.getResolvedSignature, falling back to getSymbolAtLocation +
// alias-following on the callee expression) and, when BOTH the call site's
// enclosing named function/method AND the resolved callee are named
// functions/methods/const-bound arrows declared inside the project, records
// it as a "qualifying" site. A deterministic (never random) subset of
// qualifying sites is sampled for the Go side to join onto the indexed
// graph's CALLS edges.
//
// This process never talks to Neo4j or SCIP directly —
// internal/verify/oracle/tsoracle.go runs this script and joins its JSON
// output onto real Function/Method nodes via (serviceName, filePath, name,
// container).
//
// Usage:
//   node oracle.mjs --project <absoluteProjectRoot> [--out <jsonPath>] [--ts-module <path>] [--sample-size <n>]
//
// With no --out, the JSON document is written to stdout.
//
// Exit codes:
//   0  success
//   1  usage error (missing/bad flags)
//   2  `typescript` module not found in the target project's node_modules
//      (and no --ts-module override resolved either)
//   3  the resolved `typescript` install is too old (this oracle only needs
//      stable public APIs — program, checker.getResolvedSignature,
//      checker.getSymbolAtLocation — which have been stable since well
//      before TS 5.0; we require >= 5.0 as a conservative floor rather than
//      the >= 5.4 floor tools/ts-resolver needs for isTypeAssignableTo)
//   4  unexpected internal failure (tsconfig not found, Program creation
//      failed, etc.) — printed to stderr with a stack trace

import { createRequire } from "node:module";
import { writeFileSync } from "node:fs";
import path from "node:path";

const DEFAULT_SAMPLE_SIZE = 200;

function parseArgs(argv) {
  const args = { project: null, out: null, tsModule: null, sampleSize: DEFAULT_SAMPLE_SIZE };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === "--project") {
      args.project = argv[++i];
    } else if (a === "--out") {
      args.out = argv[++i];
    } else if (a === "--ts-module") {
      args.tsModule = argv[++i];
    } else if (a === "--sample-size") {
      const n = parseInt(argv[++i], 10);
      if (Number.isNaN(n) || n <= 0) throw new UsageError("--sample-size must be a positive integer");
      args.sampleSize = n;
    } else {
      throw new UsageError(`unrecognized argument: ${a}`);
    }
  }
  if (!args.project) throw new UsageError("--project <absoluteProjectRoot> is required");
  return args;
}

class UsageError extends Error {}
class TSModuleNotFoundError extends Error {}
class TSVersionTooOldError extends Error {}

/**
 * Loads the `typescript` module. Resolution order mirrors
 * tools/ts-resolver/resolve.mjs's loadTypeScript:
 *   1. --ts-module <path>, if given — lets tests point this script at a
 *      `typescript` install outside the fixture project (fixtures
 *      deliberately do not vendor their own copy; see
 *      tools/ts-oracle/testdata/simplets/package.json).
 *   2. `typescript` resolved from <projectRoot>/package.json, i.e. the
 *      TARGET project's own node_modules.
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

function assertTypeScriptVersionFloor(ts) {
  const version = ts.version || "unknown";
  const major = parseInt(version.split(".")[0], 10);
  const minor = parseInt(version.split(".")[1], 10);
  // Floor: >= 5.0. The oracle only needs Program/TypeChecker public APIs
  // that have been stable for many major versions (getResolvedSignature,
  // getSymbolAtLocation, resolveAlias) — no need for the 5.4 floor
  // tools/ts-resolver requires for isTypeAssignableTo.
  const versionOk = !Number.isNaN(major) && (major > 5 || (major === 5 && minor >= 0));
  if (!versionOk) {
    throw new TSVersionTooOldError(`typescript ${version} is too old: oracle.mjs requires TypeScript >= 5.0`);
  }
}

function toPosixRelative(fromDir, absPath) {
  const rel = path.relative(fromDir, absPath);
  return rel.split(path.sep).join("/");
}

function isNonDeclarationProjectFile(fileName, projectRootAbs) {
  if (fileName.endsWith(".d.ts")) return false;
  const rel = path.relative(projectRootAbs, fileName);
  if (rel.startsWith("..") || path.isAbsolute(rel)) return false; // outside project root (lib.*.d.ts etc.)
  if (rel.split(path.sep).includes("node_modules")) return false;
  return true;
}

// namedEnclosure walks up from `node` to find the nearest enclosing named
// function/method/const-bound-arrow declaration. Returns
// { name, container, line, kind } or null when the position is not inside
// any named function-like construct (e.g. top-level module code).
//
// "Named" per the brief:
//   - function declarations (function foo() {})           -> name "foo", container ""
//   - class methods (method_definition-equivalent)         -> name "method", container "ClassName"
//   - const-bound arrows/function-expressions               -> name from the variable declarator
//   - object-literal method shorthand bound to a variable    -> treated as const-bound (name from declarator)
// Anonymous callbacks passed inline (arrow/function expression NOT bound to
// a declarator) are NOT named enclosures — walking continues past them to
// their own enclosing named construct, and the site is only "in" that
// outer construct if there's no named layer at all in between... but per
// the brief we skip sites whose caller enclosure is anonymous, so we must
// distinguish "no named enclosure found before hitting an anonymous
// function" from "found one". We treat the walk as: the first function-like
// node encountered going up is THE enclosure that matters (an anonymous
// callback nested inside a named outer function still means the call site's
// direct enclosure is anonymous, and per the brief such sites are skipped
// entirely — we do not attribute the call to the outer named function).
function namedEnclosure(ts, node, sourceFile) {
  let cur = node.parent;
  while (cur) {
    if (ts.isFunctionDeclaration(cur)) {
      const name = cur.name ? cur.name.text : "";
      if (name) return { name, container: "", line: lineOf(cur, sourceFile), node: cur };
      return { name: "", container: "", line: lineOf(cur, sourceFile), node: cur }; // anonymous
    }
    if (ts.isMethodDeclaration(cur) || ts.isConstructorDeclaration(cur) || ts.isGetAccessor(cur) || ts.isSetAccessor(cur)) {
      const container = enclosingClassOrInterfaceName(ts, cur);
      let name = "";
      if (ts.isConstructorDeclaration(cur)) {
        name = "constructor";
      } else if (cur.name && ts.isIdentifier(cur.name)) {
        name = cur.name.text;
      }
      if (name) return { name, container, line: lineOf(cur, sourceFile), node: cur };
      return { name: "", container: "", line: lineOf(cur, sourceFile), node: cur };
    }
    if (ts.isFunctionExpression(cur) || ts.isArrowFunction(cur)) {
      const declName = declaratorNameFor(ts, cur);
      if (declName) return { name: declName, container: "", line: lineOf(cur, sourceFile), node: cur };
      return { name: "", container: "", line: lineOf(cur, sourceFile), node: cur }; // anonymous callback
    }
    cur = cur.parent;
  }
  return null; // top-level module code: no enclosing function at all
}

// declaratorNameFor returns the bound variable name when `fn` (a
// FunctionExpression or ArrowFunction) is the direct initializer of a
// VariableDeclaration (`const g = () => {}` / `const g = function () {}`),
// or "" otherwise (inline/anonymous).
function declaratorNameFor(ts, fn) {
  const p = fn.parent;
  if (p && ts.isVariableDeclaration(p) && p.initializer === fn && ts.isIdentifier(p.name)) {
    return p.name.text;
  }
  // Object literal method shorthand bound to a variable via a property
  // assignment is NOT counted here — the brief's scope is function
  // declarations, class methods, and const-bound arrows only.
  return "";
}

function enclosingClassOrInterfaceName(ts, node) {
  let cur = node.parent;
  while (cur) {
    if (ts.isClassDeclaration(cur) || ts.isClassExpression(cur)) {
      return cur.name ? cur.name.text : "";
    }
    cur = cur.parent;
  }
  return "";
}

function lineOf(node, sourceFile) {
  return sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile)).line + 1;
}

// resolveCallee resolves a CallExpression's callee to a declaration node,
// preferring the checker's resolved signature (handles overloads and most
// call shapes precisely) and falling back to a direct symbol lookup on the
// callee expression with alias-following (handles re-exported /
// imported-and-renamed bindings that resolveSignature sometimes can't
// pin to a single declaration, e.g. when the call target's type is a
// union of function types).
function resolveCallee(ts, checker, call) {
  const signature = checker.getResolvedSignature(call);
  if (signature && signature.declaration) {
    return signature.declaration;
  }

  let symbol = checker.getSymbolAtLocation(call.expression);
  if (!symbol) return null;
  if (symbol.flags & ts.SymbolFlags.Alias) {
    symbol = checker.getAliasedSymbol(symbol);
  }
  if (!symbol.declarations || symbol.declarations.length === 0) return null;
  // Prefer a function-like declaration among the symbol's declarations
  // (a symbol can carry multiple declaration kinds, e.g. merged
  // namespace+function).
  for (const d of symbol.declarations) {
    if (
      ts.isFunctionDeclaration(d) ||
      ts.isMethodDeclaration(d) ||
      ts.isConstructorDeclaration(d) ||
      ts.isFunctionExpression(d) ||
      ts.isArrowFunction(d)
    ) {
      return d;
    }
  }
  return null;
}

// calleeIdentity extracts { name, container, line, sourceFile } from a
// resolved callee declaration node, applying the same "named" rules as
// namedEnclosure. Returns null when the declaration isn't itself a named
// function-like construct (anonymous, or a declaration kind we don't treat
// as callable-and-named, e.g. an ambient/overload signature with no body).
function calleeIdentity(ts, decl) {
  const sourceFile = decl.getSourceFile();
  if (ts.isFunctionDeclaration(decl)) {
    const name = decl.name ? decl.name.text : "";
    if (!name) return null;
    if (!decl.body) return null; // overload signature declaration, not the implementation
    return { name, container: "", line: lineOf(decl, sourceFile), sourceFile };
  }
  if (ts.isMethodDeclaration(decl) || ts.isConstructorDeclaration(decl)) {
    if (!decl.body) return null; // overload signature / abstract method: no implementation to attribute to
    const container = enclosingClassOrInterfaceName(ts, decl);
    const name = ts.isConstructorDeclaration(decl) ? "constructor" : (decl.name && ts.isIdentifier(decl.name) ? decl.name.text : "");
    if (!name) return null;
    return { name, container, line: lineOf(decl, sourceFile), sourceFile };
  }
  if (ts.isFunctionExpression(decl) || ts.isArrowFunction(decl)) {
    const declName = declaratorNameFor(ts, decl);
    if (!declName) return null; // anonymous, inline callback target
    return { name: declName, container: "", line: lineOf(decl, sourceFile), sourceFile };
  }
  return null;
}

function main() {
  const args = parseArgs(process.argv.slice(2));
  const projectRootAbs = path.resolve(args.project);

  const ts = loadTypeScript(projectRootAbs, args.tsModule);
  assertTypeScriptVersionFloor(ts);

  const configPath = ts.findConfigFile(projectRootAbs, ts.sys.fileExists, "tsconfig.json");
  if (!configPath) {
    throw new Error(`no tsconfig.json found under ${projectRootAbs}`);
  }

  const configFile = ts.readConfigFile(configPath, ts.sys.readFile);
  if (configFile.error) {
    throw new Error(`failed to read ${configPath}: ${ts.flattenDiagnosticMessageText(configFile.error.messageText, "\n")}`);
  }

  const parsed = ts.parseJsonConfigFileContent(configFile.config, ts.sys, path.dirname(configPath));

  const program = ts.createProgram({ rootNames: parsed.fileNames, options: parsed.options });
  const checker = program.getTypeChecker();

  const stats = {
    filesScanned: 0,
    callSitesSeen: 0,
    qualifying: 0,
    sampled: 0,
    skippedExternal: 0,
    skippedAnonymousCaller: 0,
    skippedAnonymousCallee: 0,
    skippedUnresolved: 0,
    skippedNoEnclosure: 0,
    skippedSuperOrDynamic: 0,
  };

  /** @type {object[]} */
  const qualifyingSites = [];

  for (const sourceFile of program.getSourceFiles()) {
    if (!isNonDeclarationProjectFile(sourceFile.fileName, projectRootAbs)) continue;
    stats.filesScanned++;
    const callerFileRel = toPosixRelative(projectRootAbs, sourceFile.fileName);

    const visit = (node) => {
      if (ts.isCallExpression(node)) {
        stats.callSitesSeen++;

        // super(...) calls and dynamic/computed callees with no resolvable
        // symbol shape (e.g. obj[key]()) are counted and skipped outright —
        // "any"-typed calls fall through to skippedUnresolved below since
        // getResolvedSignature/getSymbolAtLocation simply return nothing
        // useful for them.
        if (node.expression.kind === ts.SyntaxKind.SuperKeyword) {
          stats.skippedSuperOrDynamic++;
          ts.forEachChild(node, visit);
          return;
        }

        const enclosure = namedEnclosure(ts, node, sourceFile);
        if (!enclosure) {
          stats.skippedNoEnclosure++;
          ts.forEachChild(node, visit);
          return;
        }
        if (!enclosure.name) {
          stats.skippedAnonymousCaller++;
          ts.forEachChild(node, visit);
          return;
        }

        const calleeDecl = resolveCallee(ts, checker, node);
        if (!calleeDecl) {
          stats.skippedUnresolved++;
          ts.forEachChild(node, visit);
          return;
        }

        const calleeFileAbs = calleeDecl.getSourceFile().fileName;
        if (!isNonDeclarationProjectFile(calleeFileAbs, projectRootAbs)) {
          stats.skippedExternal++;
          ts.forEachChild(node, visit);
          return;
        }

        const callee = calleeIdentity(ts, calleeDecl);
        if (!callee) {
          stats.skippedAnonymousCallee++;
          ts.forEachChild(node, visit);
          return;
        }

        stats.qualifying++;
        qualifyingSites.push({
          callerFile: callerFileRel,
          callerName: enclosure.name,
          callerContainer: enclosure.container,
          callerLine: enclosure.line,
          calleeFile: toPosixRelative(projectRootAbs, calleeFileAbs),
          calleeName: callee.name,
          calleeContainer: callee.container,
          calleeLine: callee.line,
        });
      }
      ts.forEachChild(node, visit);
    };
    visit(sourceFile);
  }

  // Deterministic sampling: every k-th qualifying site, k chosen so the
  // sample size is hit (or all sites, if fewer qualify than requested).
  // NO Math.random — reruns against an unchanged project must produce an
  // identical sample.
  const sites = sampleDeterministic(qualifyingSites, args.sampleSize);
  stats.sampled = sites.length;

  const output = { sites, stats };
  const json = JSON.stringify(output, null, 2);
  if (args.out) {
    writeFileSync(args.out, json);
  } else {
    process.stdout.write(json + "\n");
  }
}

function sampleDeterministic(items, sampleSize) {
  if (items.length <= sampleSize) return items;
  const k = items.length / sampleSize;
  const out = [];
  for (let i = 0; i < sampleSize; i++) {
    out.push(items[Math.floor(i * k)]);
  }
  return out;
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
  process.stderr.write(`ts-oracle failed: ${e.stack || e.message}\n`);
  process.exit(4);
}
