package structure

import (
	"strings"
	"testing"
)

// extract is a test helper: parse src under the named language and fail the
// test on a hard error.
func extract(t *testing.T, lang, src string) *FileStructure {
	t.Helper()
	l, ok := ForLanguage(lang)
	if !ok {
		t.Fatalf("language %q not registered", lang)
	}
	fs, err := Extract(l, []byte(src))
	if err != nil {
		t.Fatalf("Extract(%s): %v", lang, err)
	}
	return fs
}

// fnSummary is the assertable shape of one extracted function.
type fnSummary struct {
	Kind      string
	Name      string
	StartLine int
	EndLine   int
	Parent    int
}

func summarize(fs *FileStructure) []fnSummary {
	out := make([]fnSummary, 0, len(fs.Functions))
	for _, f := range fs.Functions {
		out = append(out, fnSummary{f.Kind, f.Name, f.StartLine, f.EndLine, f.ParentIndex})
	}
	return out
}

func assertFunctions(t *testing.T, fs *FileStructure, want []fnSummary) {
	t.Helper()
	got := summarize(fs)
	if len(got) != len(want) {
		t.Fatalf("function count = %d, want %d\ngot: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("functions[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestTypeScriptStructure covers the exact failure modes of the
// declaration-order inference this package replaces: nesting, closures bound
// to consts, trailing non-function declarations, and end-of-file tails.
func TestTypeScriptStructure(t *testing.T) {
	src := `function greet(name: string): void {
  console.log(name);
}

const logger = { level: 1 };

export class ConsoleLogger {
  constructor(private prefix: string) {}

  log(message: string): void {
    const format = (m: string): string => this.prefix + m;
    console.log(format(message));
  }
}

export const createLogger = (prefix: string): ConsoleLogger => {
  return new ConsoleLogger(prefix);
};

export interface Config { verbose: boolean }
`
	fs := extract(t, "typescript", src)
	if fs.HasErrors {
		t.Fatal("unexpected parse errors")
	}
	assertFunctions(t, fs, []fnSummary{
		// greet ends at its closing brace, line 3 — not at the next
		// declaration, not at EOF.
		{"function", "greet", 1, 3, -1},
		{"method", "constructor", 8, 8, -1},
		{"method", "log", 10, 13, -1},
		// The arrow nested inside log() is attributed to log, and its span
		// widens to the declarator so `format`'s identifier is inside it.
		{"closure", "format", 11, 11, 2},
		// createLogger's span starts at its declarator (the identifier),
		// and ends before the trailing interface.
		{"closure", "createLogger", 16, 18, -1},
	})

	// The interface after createLogger must not extend anyone's range: no
	// function's EndLine reaches line 20.
	for _, f := range fs.Functions {
		if f.EndLine >= 20 {
			t.Errorf("%s.EndLine = %d extends into trailing declarations", f.Name, f.EndLine)
		}
	}
}

// TestTypeScriptTypedArrowRegression pins the construct that broke the
// pure-Go runtime candidate (RFC-010 §8): a typed parameter AND a return
// type on an arrow function. The official grammar must parse it clean.
func TestTypeScriptTypedArrowRegression(t *testing.T) {
	fs := extract(t, "typescript", "const g = (a: number): number => a;\n")
	if fs.HasErrors {
		t.Fatal("official grammar failed the (a: X): Y => repro — runtime regression")
	}
	assertFunctions(t, fs, []fnSummary{{"closure", "g", 1, 1, -1}})
}

// TestErrorRecoveryPartialExtraction: a file with one broken region still
// yields exact spans for intact functions, and HasErrors tells the caller to
// fall back per definition — not per file.
func TestErrorRecoveryPartialExtraction(t *testing.T) {
	src := `function ok(): void {
  console.log("fine");
}

function broken( {{{

function alsoOk(): void {
  console.log("fine too");
}
`
	fs := extract(t, "typescript", src)
	if !fs.HasErrors {
		t.Fatal("expected HasErrors on malformed input")
	}
	var names []string
	for _, f := range fs.Functions {
		if f.Name != "" {
			names = append(names, f.Name)
		}
	}
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "ok") {
		t.Errorf("intact function lost to unrelated syntax error: %v", names)
	}
	for _, f := range fs.Functions {
		if f.Name == "ok" && (f.StartLine != 1 || f.EndLine != 3) {
			t.Errorf("ok span = %d-%d, want 1-3 despite later error", f.StartLine, f.EndLine)
		}
	}
}

// TestInnermostAt verifies position→function attribution, the §4.3
// mechanism: identifier positions land in the right (innermost) function,
// including closures widened to their declarator.
func TestInnermostAt(t *testing.T) {
	src := `function outer(): void {
  const inner = (): void => {
    console.log("deep");
  };
  inner();
}

const top = 1;
`
	fs := extract(t, "typescript", src)
	if fs.HasErrors {
		t.Fatal("unexpected parse errors")
	}

	cases := []struct {
		name      string
		line, col int
		wantName  string
		wantOK    bool
	}{
		{"outer body", 5, 2, "outer", true},
		{"identifier of inner (declarator widening)", 2, 8, "inner", true},
		{"deep inside inner", 3, 4, "inner", true},
		{"outside all functions", 8, 0, "", false},
		{"line-only query inside inner", 3, -1, "inner", true},
	}
	for _, c := range cases {
		idx, ok := fs.InnermostAt(c.line, c.col)
		if ok != c.wantOK {
			t.Errorf("%s: ok=%v want %v", c.name, ok, c.wantOK)
			continue
		}
		if ok && fs.Functions[idx].Name != c.wantName {
			t.Errorf("%s: got %q want %q", c.name, fs.Functions[idx].Name, c.wantName)
		}
	}
}

// TestAllLanguagesExtract exercises every registered grammar with an
// idiomatic snippet, asserting known-positive extraction — a snippet that
// parses but yields nothing would otherwise pass silently.
func TestAllLanguagesExtract(t *testing.T) {
	cases := []struct {
		lang string
		src  string
		want []fnSummary
	}{
		{"javascript", `function f(a) { return a; }
const g = (x) => x * 2;
class C {
  m() { return 1; }
}
`, []fnSummary{
			{"function", "f", 1, 1, -1},
			{"closure", "g", 2, 2, -1},
			{"method", "m", 4, 4, -1},
		}},
		{"tsx", `export const App = (): JSX.Element => {
  return <div onClick={() => go()}>hi</div>;
};
function helper(): number { return 1; }
`, []fnSummary{
			{"closure", "App", 1, 3, -1},
			{"closure", "", 2, 2, 0},
			{"function", "helper", 4, 4, -1},
		}},
		{"python", `def f(x):
    def inner(y):
        return y
    return inner

class C:
    def m(self):
        return 1

g = lambda x: x
`, []fnSummary{
			{"function", "f", 1, 4, -1},
			{"function", "inner", 2, 3, 0},
			{"function", "m", 7, 8, -1},
			{"closure", "g", 10, 10, -1},
		}},
		{"java", `class C {
  C(int x) { this.x = x; }
  int m() {
    Runnable r = () -> System.out.println("hi");
    return 1;
  }
}
`, []fnSummary{
			{"method", "C", 2, 2, -1},
			{"method", "m", 3, 6, -1},
			{"closure", "r", 4, 4, 1},
		}},
		{"scala", `object O {
  def f(x: Int): Int = {
    val g = (y: Int) => y + 1
    g(x)
  }
}
`, []fnSummary{
			{"function", "f", 2, 5, -1},
			{"closure", "g", 3, 3, 0},
		}},
		{"kotlin", `fun f(x: Int): Int {
    val g = { y: Int -> y + 1 }
    return g(x)
}

class C {
    fun m() = 2
}
`, []fnSummary{
			{"function", "f", 1, 4, -1},
			{"closure", "g", 2, 2, 0},
			{"function", "m", 7, 7, -1},
		}},
		{"php", `<?php
function f($a) { return $a; }
class C {
  function m() { return 1; }
}
$g = function ($x) { return $x; };
$h = fn($x) => $x + 1;
`, []fnSummary{
			{"function", "f", 2, 2, -1},
			{"method", "m", 4, 4, -1},
			{"closure", "$g", 6, 6, -1},
			{"closure", "$h", 7, 7, -1},
		}},
	}
	for _, c := range cases {
		t.Run(c.lang, func(t *testing.T) {
			fs := extract(t, c.lang, c.src)
			if fs.HasErrors {
				t.Fatalf("%s snippet has parse errors", c.lang)
			}
			assertFunctions(t, fs, c.want)
		})
	}
}

// TestForFile routes extensions to grammars and reports unknowns honestly.
func TestForFile(t *testing.T) {
	for path, want := range map[string]string{
		"src/index.ts":     "typescript",
		"src/App.TSX":      "tsx",
		"lib/util.mjs":     "javascript",
		"pkg/mod.py":       "python",
		"com/x/Main.java":  "java",
		"core/Engine.kt":   "kotlin",
		"app/svc.scala":    "scala",
		"public/index.php": "php",
	} {
		l, ok := ForFile(path)
		if !ok || l.Name() != want {
			t.Errorf("ForFile(%q) = %v,%v want %s", path, l, ok, want)
		}
	}
	for _, path := range []string{"main.go", "README.md", "Makefile", "x.rs"} {
		if _, ok := ForFile(path); ok {
			t.Errorf("ForFile(%q) should have no grammar", path)
		}
	}
}

// decoSummary is the assertable shape of one DecoratorInfo.
type decoSummary struct {
	Name string
	Arg  string
}

func summarizeDecorators(ds []DecoratorInfo) []decoSummary {
	out := make([]decoSummary, 0, len(ds))
	for _, d := range ds {
		out = append(out, decoSummary{d.Name, d.Arg})
	}
	return out
}

func assertDecorators(t *testing.T, got []DecoratorInfo, want []decoSummary) {
	t.Helper()
	gs := summarizeDecorators(got)
	if len(gs) != len(want) {
		t.Fatalf("decorator count = %d, want %d\ngot: %+v want: %+v", len(gs), len(want), gs, want)
	}
	for i := range want {
		if gs[i] != want[i] {
			t.Errorf("decorators[%d] = %+v, want %+v", i, gs[i], want[i])
		}
	}
}

// funcByName finds the first FunctionNode with the given name, failing the
// test if absent.
func funcByName(t *testing.T, fs *FileStructure, name string) FunctionNode {
	t.Helper()
	for _, f := range fs.Functions {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("no function named %q in %+v", name, summarize(fs))
	return FunctionNode{}
}

// TestDecoratorExtraction_MethodAndClass covers the NestJS shape RFC-005
// targets: a decorated controller class with decorated and plain methods,
// asserting exact name/arg extraction for both stacked-string-arg and
// no-arg decorators, and that a plain method carries none.
func TestDecoratorExtraction_MethodAndClass(t *testing.T) {
	src := `@Controller('users')
class UsersController {
  @Get() findAll() {}
  @Get(':id') findOne() {}
  @Post('create') create() {}
  plainMethod() {}
}

@Injectable()
class UsersService {
  save() {}
}

function freeFunction() {}

class Scheduler {
  @EventPattern('evt') handleEvt() {}
  @Cron('* * * * *') runJob() {}
}
`
	fs := extract(t, "typescript", src)
	if fs.HasErrors {
		t.Fatal("unexpected parse errors")
	}

	// Method-level decorators, exact name/arg pairs.
	assertDecorators(t, funcByName(t, fs, "findAll").Decorators, []decoSummary{{"Get", ""}})
	assertDecorators(t, funcByName(t, fs, "findOne").Decorators, []decoSummary{{"Get", ":id"}})
	assertDecorators(t, funcByName(t, fs, "create").Decorators, []decoSummary{{"Post", "create"}})
	assertDecorators(t, funcByName(t, fs, "plainMethod").Decorators, nil)
	assertDecorators(t, funcByName(t, fs, "save").Decorators, nil)
	assertDecorators(t, funcByName(t, fs, "freeFunction").Decorators, nil)
	assertDecorators(t, funcByName(t, fs, "handleEvt").Decorators, []decoSummary{{"EventPattern", "evt"}})
	assertDecorators(t, funcByName(t, fs, "runJob").Decorators, []decoSummary{{"Cron", "* * * * *"}})

	// Class-level decorators.
	if len(fs.Classes) != 3 {
		t.Fatalf("want 3 classes, got %d: %+v", len(fs.Classes), fs.Classes)
	}
	classByName := func(name string) ClassNode {
		t.Helper()
		for _, c := range fs.Classes {
			if c.Name == name {
				return c
			}
		}
		t.Fatalf("no class named %q in %+v", name, fs.Classes)
		return ClassNode{}
	}
	assertDecorators(t, classByName("UsersController").Decorators, []decoSummary{{"Controller", "users"}})
	assertDecorators(t, classByName("UsersService").Decorators, []decoSummary{{"Injectable", ""}})
	assertDecorators(t, classByName("Scheduler").Decorators, nil)

	// Method -> enclosing-class attribution: findOne's identifier position
	// must resolve, via ClassDecoratorsAt, to UsersController's decorators —
	// not UsersService's or Scheduler's.
	findOne := funcByName(t, fs, "findOne")
	classDecos := fs.ClassDecoratorsAt(findOne.StartLine, findOne.StartCol)
	assertDecorators(t, classDecos, []decoSummary{{"Controller", "users"}})

	// A method with no enclosing class (freeFunction) resolves no class
	// decorators.
	free := funcByName(t, fs, "freeFunction")
	if got := fs.ClassDecoratorsAt(free.StartLine, free.StartCol); got != nil {
		t.Errorf("freeFunction ClassDecoratorsAt = %+v, want nil", got)
	}

	// runJob (inside Scheduler, which has no class decorator) resolves to
	// an empty decorator list, distinguishing "no enclosing class" from
	// "enclosing class has no decorators".
	runJob := funcByName(t, fs, "runJob")
	if got := fs.ClassDecoratorsAt(runJob.StartLine, runJob.StartCol); got != nil {
		t.Errorf("runJob (Scheduler has no class decorator) ClassDecoratorsAt = %+v, want nil", got)
	}
}

// TestDecoratorExtraction_NoGrammarSupport verifies decorator extraction is
// gated by language: JavaScript and other non-TS grammars never populate
// Decorators/Classes even when asked (JS has no decorator syntax in the
// stable grammar; other languages have no decorator concept at all).
func TestDecoratorExtraction_NoGrammarSupport(t *testing.T) {
	fs := extract(t, "javascript", `class C {
  m() { return 1; }
}
`)
	if fs.HasErrors {
		t.Fatal("unexpected parse errors")
	}
	if len(fs.Classes) != 0 {
		t.Errorf("javascript: want 0 classes tracked, got %d: %+v", len(fs.Classes), fs.Classes)
	}
	m := funcByName(t, fs, "m")
	if m.Decorators != nil {
		t.Errorf("javascript method Decorators = %+v, want nil", m.Decorators)
	}
}

// TestDecoratorExtraction_TSX verifies the TSX grammar (used for .tsx files)
// extracts decorators identically to plain TypeScript.
func TestDecoratorExtraction_TSX(t *testing.T) {
	fs := extract(t, "tsx", `@Controller('widgets')
class WidgetController {
  @Get(':id') get() {}
}
`)
	if fs.HasErrors {
		t.Fatal("unexpected parse errors")
	}
	assertDecorators(t, funcByName(t, fs, "get").Decorators, []decoSummary{{"Get", ":id"}})
	if len(fs.Classes) != 1 || fs.Classes[0].Name != "WidgetController" {
		t.Fatalf("want 1 class WidgetController, got %+v", fs.Classes)
	}
	assertDecorators(t, fs.Classes[0].Decorators, []decoSummary{{"Controller", "widgets"}})
}

// TestDecoratorExtraction_StackedAndNonLiteralArgs covers multiple stacked
// decorators on one target and a non-string-literal first argument (a bare
// identifier), which must yield an empty Arg rather than a guess.
func TestDecoratorExtraction_StackedAndNonLiteralArgs(t *testing.T) {
	fs := extract(t, "typescript", `const ROUTE = 'dynamic';
class C {
  @UseGuards() @Get(ROUTE) mixed() {}
}
`)
	if fs.HasErrors {
		t.Fatal("unexpected parse errors")
	}
	assertDecorators(t, funcByName(t, fs, "mixed").Decorators, []decoSummary{
		{"UseGuards", ""},
		{"Get", ""}, // non-literal arg (identifier) yields "", not a guess
	})
}

// TestByteRangesMatchSource: byte offsets must slice the source to exactly
// the function text — downstream `source` retrieval depends on it.
func TestByteRangesMatchSource(t *testing.T) {
	src := "function a(): void {}\n\nfunction b(): void {\n  a();\n}\n"
	fs := extract(t, "typescript", src)
	if len(fs.Functions) != 2 {
		t.Fatalf("want 2 functions, got %d", len(fs.Functions))
	}
	if got := src[fs.Functions[0].StartByte:fs.Functions[0].EndByte]; got != "function a(): void {}" {
		t.Errorf("a text = %q", got)
	}
	if got := src[fs.Functions[1].StartByte:fs.Functions[1].EndByte]; got != "function b(): void {\n  a();\n}" {
		t.Errorf("b text = %q", got)
	}
}
