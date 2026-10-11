package tests

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"undalang/compiler"
	"undalang/runner"
)

// The native backend must be observably identical to the interpreter: each
// program below runs through both and stdout, stderr and the exit code are
// compared byte for byte.
var differentialPrograms = map[string]string{
	"numbers": `println(1 + 2 * 3, 7 / 2, -7 / 2, 7 % 3, -7 % 3)
println(0.1 + 0.2, 10 / 4.0, 100.0, 1.5 * 0.0000001, 1000000.0, 123456.0, 2.0 * 1000000000000000000000.0)
println(1 == 1.0, 2 < 2.5, 3 >= 3, 1.5 != 1.5, -2.5, -(3))
println(9223372036854775807 + 1, 3 * -4)
println("a" + "b", "x" == "x", "x" != "y", nil == nil, true == !false)
println(1 / 3.0, 2.0 / 3.0, 0.000001, 0.0001)
`,
	"closures": `fn make_counter() {
    let mut count = 0
    return fn() {
        count = count + 1
        return count
    }
}
let c = make_counter()
c()
c()
println("counter", c())

fn fib(n) {
    if n < 2 { return n }
    return fib(n - 1) + fib(n - 2)
}
println("fib", fib(20))

fn is_even(n) { if n == 0 { true } else { is_odd(n - 1) } }
fn is_odd(n) { if n == 0 { false } else { is_even(n - 1) } }
println(is_even(10), is_odd(7))

let x = 1
fn shadow() {
    let x = 2
    if true {
        let x = 3
        println("inner", x)
    }
    return x
}
println("shadow", shadow(), x)

let compose = fn(f, g) { fn(v) { f(g(v)) } }
let inc = fn(v) { v + 1 }
let dbl = fn(v) { v * 2 }
println(compose(inc, dbl)(5))
`,
	"control": `let grade = fn(score) {
    match score {
        100 -> "perfect",
        0 -> "zero",
        _ -> if score > 50 { "pass" } else { "fail" },
    }
}
println(grade(100), grade(0), grade(70), grade(20))
let v = if false { 1 }
println(v)
fn implicit() { let a = 5 }
println(implicit())
let m = match "z" { "a" -> 1 }
println(m)
println(match [1, 2] { [1, 2] -> "array match", _ -> "no" })
println(match 1 { 1.0 -> "float", _ -> "strict" })
println(false && 1 / 0 == 1, true || 1 / 0 == 1)
println(nil && 1, 1 && nil, "s" || false)
`,
	"collections": `import "arrays" as arrays
import "maps" as maps
let m = {"b": 2, "a": [1, 2.5, "x"], 3: true, false: nil}
println(m)
println(m["a"][1], m[3], m["missing"], [1, 2][5], "hello"[1], "hi"[9])
println(maps.keys(m), maps.values({"z": 1, "y": 2}))
println(maps.get(m, "b"), maps.get(m, "q", "default"), maps.has(m, 3))
println(maps.set({"k": 1}, "k", 2), maps.merge({"a": 1, "b": 1}, {"b": 2}))
println(len(m), len([]), len("héllo"))
println({"a": 1, "a": 2})
println([1, [2, 3]] == [1, [2, 3]], {"a": [1]} == {"a": [1]}, {"a": 1} == {"a": 2})
let nums = arrays.range(1, 6)
println(nums, arrays.range(3, 0), arrays.reverse(nums), arrays.slice(nums, 1, 3))
println(arrays.map(nums, fn(n) { n * n }), arrays.filter(nums, fn(n) { n % 2 == 0 }))
println(arrays.reduce(nums, 0, fn(acc, n) { acc + n }), arrays.first([]), arrays.last(nums))
println(arrays.append(nums, 6), arrays.join(["a", "b"], "+"))
`,
	"strings": `import "strings" as strings
println(strings.upper("ığüşöç istanbul"), strings.lower("IĞÜŞÖÇ İSTANBUL"))
println(strings.upper("straße ÿ µ"), strings.lower("ΑΒΓ Привет"))
println("[" + strings.trim("  \t pad\n ") + "]", strings.split("a,b,,c", ","), strings.split("héj", ""))
println(strings.split("", ","), strings.replace("aaa", "a", "bb"), strings.replace("ab", "", "-"))
println(strings.join(["x", "y", "z"], ", "), strings.contains("team", "ea"), strings.contains("x", ""))
println(strings.has_prefix("unda", "un"), strings.has_suffix("unda", "da"), strings.has_suffix("a", "abc"))
println("unda,go" |> strings.split(",") |> strings.join("-") |> strings.upper)
`,
	"json": `import "json" as json
println(json.stringify({"name": "unda", "n": 1, "f": 2.5, "big": 1000000000000000000000.0, "tiny": 0.0000001, "list": [true, nil, "q\"<&>\n"]}))
println(json.stringify([]), json.stringify({}), json.stringify(-0.5), json.stringify(3.0))
let parsed = json.parse("{\"a\": [1, 2.5, -3e2, true, null], \"s\": \"\\u00e7\\n\", \"o\": {}}")
println(parsed, type(parsed["a"][2]), parsed["s"])
println(json.stringify(parsed))
`,
	"math_and_types": `import "math" as math
import "strings" as strings
println(math.abs(-3), math.ceil(1.2), math.floor(-1.2), math.round(2.5), math.round(-2.5), math.sqrt(16))
println(math.max(1, 2.5), math.min(-1, 3), math.pow(2, 10))
fn named(a, b) { return a + b }
println(type(1), type(1.5), type("s"), type(true), type(nil), type([]), type({}), type(named), type(len), type(strings))
println(named)
println(fn(x) { x })
println(len, strings.upper)
`,
	"dynamic_scope": `let x = "outer"
fn f() {
    let g = fn() { return x }
    let before = g()
    let x = "inner"
    return before + "/" + g()
}
println(f())
fn uses_len() { return len([1, 2, 3]) }
println(uses_len())
let len = fn(v) { "shadowed" }
println(uses_len())
let mut total = 0
let add = fn(n) { total = total + n }
add(5)
add(7)
println(total)
`,
	"top_level_return": `println("before")
return 1
println("after")
`,
	"shell": `import "shell" as shell
import "process" as process
print(shell.run("echo hi; echo err 1>&2"))
print(process.run("printf", "%s-%s", "a", "b"))
println(process.env("UNDA_TEST_UNSET_VARIABLE") == "")
`,
	"loops": `// comments are ignored
import "arrays" as arrays
let mut i = 0 // trailing comment
while i < 6 {
    i = i + 1
    if i == 2 { continue }
    if i == 5 { break }
    print(i, "")
}
println()
for x in [10, 20] { print(x, "") }
for i, x in ["a", "b"] { print(i, x, "") }
for k in {"b": 2, "a": 1} { print(k, "") }
for k, v in {"b": 2, "a": 1, 3: nil} { print(k, v, "") }
for c in "héllo" { print(c, "") }
for i, c in "ğü" { print(i, c, "") }
println()

// closures capture each iteration's bindings
let mut fns = []
for n in [1, 2, 3] {
    let doubled = n * 2
    fns = arrays.append(fns, fn() { n + doubled })
}
println(arrays.map(fns, fn(f) { f() }))
let mut j = 0
let mut ws = []
while j < 3 {
    let snapshot = j
    ws = arrays.append(ws, fn() { snapshot })
    j = j + 1
}
println(arrays.map(ws, fn(f) { f() }))

// nested loops, break inside if/match expressions, return from loops
let mut pairs = []
for a in arrays.range(0, 4) {
    for b in arrays.range(0, 4) {
        if b > a { break }
        let skip = match b { 1 -> if true { continue }, _ -> false }
        pairs = arrays.append(pairs, [a, b])
    }
}
println(pairs)
fn find(xs, target) {
    for i, x in xs { if x == target { return i } }
    return -1
}
println(find(["x", "y", "z"], "z"), find([], 1))
let mut total = 0
for x in arrays.range(0, 100000) { total = total + x }
println(total, 1 + if true { 5 } else { 0 })
fn counter() {
    let mut count = 0
    let inc = fn() { count = count + 1 }
    for _x in [1, 2, 3] { inc() }
    return count
}
println(counter())
`,
	"err_iterate": "fn it(v) { for x in v { x } }\nit(5)",
	"err_in_loop": "let mut i = 0\nwhile true {\n    i = i + 1\n    if i == 3 { i + nil }\n}",
	"patterns": `import "arrays" as arrays
fn describe(v) {
    match v {
        0 -> "zero",
        n if type(n) == "integer" && n < 0 -> "negative " + type(n),
        [] -> "empty array",
        [x] -> "one: " + type(x),
        [first, ..rest] -> "first " + type(first) + " rest " + type(rest) + " " + type(len(rest)),
        {"name": name, "age": age} if age >= 18 -> name + " (adult)",
        {"name": name} -> name,
        {} -> "some map",
        s if type(s) == "string" -> "string " + s,
        _ -> "other",
    }
}
for v in [0, -3, [], [1], [1, 2, 3], {"name": "Ada", "age": 36}, {"name": "Bo", "age": 5}, {"x": 1}, "hi", 4.5] {
    println(describe(v))
}
println(match [1, [2, 3]] { [a, [b, c]] -> a + b + c })
println(match [1, 2, 3] { [a, ..] -> a }, match [1, 2, 3] { [_, ..tail] -> tail })
println(match {"k": [1, {"deep": "yes"}]} { {"k": [_, {"deep": d}]} -> d })
println(match 5 { 1 -> "one" })
println(match "x" { "x" if false -> "guarded", "x" -> "plain" })
let limit = 3
println(match 3 { x if x == limit -> "equals limit", _ -> "no" })
let mut adders = []
for n in [1, 2] {
    adders = arrays.append(adders, match n { k -> fn(v) { v + k } })
}
println(adders[0](10), adders[1](10))
fn sum(xs) { match xs { [] -> 0, [h, ..t] -> h + sum(t) } }
println(sum([1, 2, 3, 4]))
`,
	"err_pattern_key": "fn k(key) { match {} { {key: v} -> v } }\nk([1])",
	"err_guard":       "match [1] { n if n > 0 -> n }",
	"unicode_strings": "let s = \"çay ☕ 🍵\"\nprintln(len(s), s[0], s[4], s[6], s[7], s[-1], len(\"\"))\nprintln(len(\"ab\"), \"日本語\"[2])\n",
	"err_type_mismatch": `fn f(v) { return v + "a" }
println("start")
f(1)`,
	"err_arity":          "fn call(f) { f(1) }\ncall(fn(a, b) { a })",
	"err_not_function":   "fn call(v) { v() }\ncall(5)",
	"err_div_zero":       "fn d(a, b) { a / b }\nd(1, 0)",
	"err_mod_float":      "fn d(a, b) { a % b }\nd(1.5, 2)",
	"err_depth":          "fn loop(n) { return loop(n + 1) }\nloop(0)",
	"err_index":          "fn i(a, b) { a[b] }\ni([1], \"x\")",
	"err_map_key":        "fn k(v) { {v: 1} }\nk([1])",
	"err_member":         "fn m(v) { v.name }\nm(5)",
	"map_members":        "let user = {\"name\": \"ada\", \"tags\": {\"lang\": \"unda\"}}\nprintln(user.name, user.tags.lang, user.missing)\nfor u in [user] { println(u.name) }\n",
	"err_neg":            "fn n(v) { -v }\nn(\"s\")",
	"err_builtin_arg":    "import \"strings\" as strings\nfn u(v) { strings.upper(v) }\nu(5)",
	"err_builtin_arity":  "fn l(f) { f(1, 2) }\nl(len)",
	"err_in_callback":    "import \"arrays\" as arrays\narrays.map([1, 2], fn(v) { v + nil })",
	"err_undefined_late": "fn g() { later }\ng()\nlet later = 1",
	"err_immutable_late": "let mut x = 1\nfn f() {\n    let r = fn() { x = 2 }\n    r()\n    println(x)\n    let x = 5\n    r()\n}\nf()",
	"err_read_file":      "read_file(\"/nonexistent/unda/file\")",
	"err_json":           "import \"json\" as json\njson.parse(\"[1, 2\")",
	"err_slice":          "import \"arrays\" as arrays\nfn s(a) { arrays.slice(a, 2, 1) }\ns([1, 2, 3])",
	"err_shell":          "import \"shell\" as shell\nshell.run(\"echo out; exit 3\")",
	"err_unknown_op":     "fn s(a, b) { a - b }\ns(\"x\", \"y\")",
	"err_string_and":     "fn s(a, b) { a && b }\ns(\"x\", \"y\")",
}

func TestNativeMatchesInterpreter(t *testing.T) {
	cc := requireCC(t)
	for name, src := range differentialPrograms {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, name+".unda")
			if err := os.WriteFile(path, []byte(src), 0644); err != nil {
				t.Fatal(err)
			}
			compareBackends(t, cc, path)
		})
	}
}

func TestNativeLocalImport(t *testing.T) {
	cc := requireCC(t)
	dir := t.TempDir()
	write := func(name, src string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("lib/util.unda", `import "helpers" as helpers
let version = 2
fn greet(name) { return helpers.prefix() + name }
fn fail(v) { return v * "x" }
`)
	write("lib/helpers.unda", `fn prefix() { return "hi " }`)
	write("main.unda", `import "lib/util" as util
import "lib/util" as again
println(util.greet("unda"), util.version, again.version)
println(util)
util.fail(2)
`)
	compareBackends(t, cc, filepath.Join(dir, "main.unda"))
}

func TestNativeExamples(t *testing.T) {
	cc := requireCC(t)
	for _, name := range []string{"phase0", "mutability", "phase1_data_pipeline"} {
		t.Run(name, func(t *testing.T) {
			compareBackends(t, cc, filepath.Join("..", "examples", name+".unda"))
		})
	}
}

// gcStressThreshold makes native test binaries collect every few KiB;
// set UNDA_TEST_GC_THRESHOLD=1 to collect on every allocation.
var gcStressThreshold = func() string {
	if v := os.Getenv("UNDA_TEST_GC_THRESHOLD"); v != "" {
		return v
	}
	return "4096"
}()

// A loop that allocates far more than it keeps must run in bounded memory.
func TestNativeGarbageCollection(t *testing.T) {
	cc := requireCC(t)
	if runtime.GOOS != "linux" {
		t.Skip("max RSS check needs Linux")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "churn.unda")
	src := `import "arrays" as arrays
let mut kept = []
let mut i = 0
while i < 2000000 {
    let tmp = {"n": i, "s": "value-" + "x", "list": [i, i + 1]}
    if i % 100000 == 0 { kept = arrays.append(kept, tmp) }
    i = i + 1
}
println(len(kept), kept[19]["n"], kept[19]["list"])
`
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "churn")
	if err := compiler.Build(path, compiler.Options{Output: exe, CC: cc}); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "20 1900000 [1900000, 1900001]\n" {
		t.Fatalf("wrong output %q", out)
	}
	usage, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage)
	if !ok {
		t.Skip("no rusage")
	}
	if maxRSSKB := usage.Maxrss; maxRSSKB > 100*1024 {
		t.Fatalf("max RSS %d KB; garbage is not being collected", maxRSSKB)
	}
}

func requireCC(t *testing.T) string {
	t.Helper()
	cc := os.Getenv("CC")
	if cc == "" {
		cc = "cc"
	}
	if _, err := exec.LookPath(cc); err != nil {
		t.Skipf("no C compiler available (%s)", cc)
	}
	return cc
}

type runResult struct {
	stdout, stderr string
	code           int
}

func compareBackends(t *testing.T, cc, path string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := runner.RunFile(path, &out, &errOut)
	interp := runResult{out.String(), errOut.String(), code}

	exe := filepath.Join(t.TempDir(), "prog")
	if err := compiler.Build(path, compiler.Options{Output: exe, CC: cc}); err != nil {
		var diagErr *compiler.DiagnosticsError
		if errors.As(err, &diagErr) {
			t.Fatalf("build failed with diagnostics: %v", diagErr.Program.Diagnostics)
		}
		t.Fatalf("build failed: %v", err)
	}
	cmd := exec.Command(exe)
	// Collect very often so a missed GC root shows up as a mismatch.
	cmd.Env = append(os.Environ(), "UNDA_GC_THRESHOLD="+gcStressThreshold)
	var nout, nerr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &nout, &nerr
	ncode := 0
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("running native binary: %v", err)
		}
		ncode = exitErr.ExitCode()
	}
	native := runResult{nout.String(), nerr.String(), ncode}

	if interp != native {
		t.Fatalf("backends differ\n--- interpreter (exit %d)\nstdout:\n%s\nstderr:\n%s\n--- native (exit %d)\nstdout:\n%s\nstderr:\n%s",
			interp.code, interp.stdout, interp.stderr, native.code, native.stdout, native.stderr)
	}
	if strings.HasPrefix(filepath.Base(path), "err_") && (interp.code == 0 || !strings.Contains(interp.stderr, "runtime error: ")) {
		t.Fatalf("expected a runtime error, got exit %d stderr %q", interp.code, interp.stderr)
	}
}
