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

// the native backend has to behave exactly like the interpreter. every
// program below runs through both and stdout, stderr and the exit code get
// compared byte for byte
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
	"typed": `import "arrays" as arrays
fn add(a: int, b: int) -> int { a + b }
fn half(n: number) -> float { n / 2.0 }
let mut total: int = 0
for x in [1, 2, 3] { total = add(total, x) }
println(total, half(3), half(2.5))
let greet = fn(name: string) -> string { "hi " + name }
println(greet("unda"), "unda" |> greet)
println(add)
println(greet)
fn maybe(flag: bool) -> any { if flag { 1 } else { nil } }
println(maybe(true), maybe(false))
let f: fn = len
println(f("abc"), arrays.map([1, 2], fn(v: int) -> int { v * 10 }))
fn first(xs: array) -> any { for x in xs { return x } }
println(first([7, 8]), first([]))
`,
	"results": `import "strings" as strings
fn parse_age(s: string) -> result {
    let n = match s { "" -> nil, "36" -> 36, "five" -> nil, _ -> -1 }
    if n == nil { return err("not a number: " + s) }
    if n < 0 { return err("negative") }
    ok(n)
}
fn describe(s) -> result {
    let age = parse_age(s)?
    ok(strings.upper(s) + " is " + type(age) + " " + "!")
}
for s in ["36", "five", "-1"] {
    println(describe(s), match describe(s) { ok(v) -> v, err(e) -> "failed: " + e })
}
println(ok(1) == ok(1), ok(1) == err(1), err([1, 2]), type(ok(nil)), ok(ok(2)))
let r: result = err({"code": 3})
println(match r { err({"code": c}) if c > 2 -> c, _ -> 0 })
fn first_ok(rs) { for r in rs { let v = r?
println("got", v) }
"all ok" }
println(first_ok([ok(1), ok(2)]), first_ok([ok(1), err("stop"), ok(3)]))
`,
	"try_builtins": `import "json" as json
import "shell" as shell
import "process" as process
println(try_read_file("/nonexistent/unda/file"))
println(try_read_file("/dev/null"), try_write_file("/dev/null", "abc"), try_append_file("/dev/null", "x"))
println(try_write_file("/nonexistent/unda/file", "x"))
println(try_append_file("/nonexistent/unda/file", "x"))
println(json.try_parse("{\"a\": [1, 2.5, null]}"), json.try_parse("[1, 2"), json.try_parse("1 x"))
println(shell.try_run("echo hi"), shell.try_run("echo out; exit 3"))
println(process.try_run("unda-no-such-command-xyz"), process.try_run("sh", "-c", "echo yes"))
fn load(path) -> result {
    let text = try_read_file(path)?
    let data = json.try_parse(text)?
    ok(data)
}
println(load("/nonexistent/unda/config.json"))
match load("/dev/null") { ok(v) -> println("ok", v), err(e) -> println("bad:", e) }
`,
	"err_try_arg":   "fn r(p) { try_read_file(p) }\nr(5)",
	"err_try_arity": "import \"json\" as json\nfn p(f) { f(1, 2) }\np(json.try_parse)",
	"fast_ints": `fn calc(a, b) { [a + b, a - b, a * b, a / b, a % b, a < b, a >= b, a == b, a != b] }
println(calc(7, 2), calc(-7, 2), calc(7, -2), calc(-7, -2))
println(calc(9223372036854775807, 1), calc(-9223372036854775807 - 1, -1))
println(calc(7, 2.5), calc(1.5, 2), calc("a", "a")[7], -(-9223372036854775807 - 1))
println(2 * 3 + 4, 1 - 9223372036854775807 - 2, 3 < 4, 5 == 5, -(2 + 3))
fn twice(f, v) { f(f(v)) }
fn inc(n) { n + 1 }
println(twice(inc, 1), 5 |> inc, inc(1) |> twice(inc))
fn redefined() { 1 }
let redefined = fn() { 2 }
println(redefined())
`,
	"err_fast_div":         "fn d(a, b) { a / b }\nprintln(d(7, 2))\nd(1, 0)",
	"err_fast_mod":         "fn d(a, b) { a % b }\nprintln(d(-7, 3))\nd(1, 0)",
	"err_fast_mismatch":    "fn s(a, b) { a < b }\nprintln(s(1, 2))\ns(1, \"x\")",
	"err_direct_too_early": "fn a() { b() }\na()\nfn b() { 1 }",
	"err_direct_depth":     "fn down(n) { down(n + 1) }\ndown(0)",
	"structs": `import "arrays" as arrays
struct Point { x: number, y: number }
struct Shape {
    name: string
    at: Point
    tags
}
let p = Point(1, 2.5)
let s = Shape(tags: [], at: p, name: "dot")
println(p, s, p.x + p.y, s.at.y, type(p), type(Point), Point)
println(p == Point(1, 2.5), p == Point(1, 2), p != s, [p] == [Point(1, 2.5)])
let mut moved = s
moved.at.x = 10
moved.tags = ["new"]
println(moved, s)
fn shift(pt: Point, by: number) -> Point { Point(pt.x + by, pt.y + by) }
println(shift(p, 1), arrays.map([1, 2], fn(n) { Point(n, n) }), arrays.map([[3, 4]], fn(a) { match a { [x, y] -> Point(x, y) } }))
let mut cfg = {"db": {"port": 1}}
cfg.db.port = 2
cfg.db.host = "x"
cfg.name = "app"
println(cfg)
for pair in [Point(0, 0), Point(3, 4)] { println(pair.x * pair.x + pair.y * pair.y) }
fn exact(v) { match v { Point(1, 2.5) -> "exact", _ -> "other" } }
let any_v: any = p
println(any_v, exact(p), exact(Point(0, 0)))
`,
	"err_struct_member":    "struct P { a }\nfn get(v) { v.b }\nget(P(1))",
	"err_struct_ctor_type": "struct P { a: int }\nfn mk(v) { P(v) }\nprintln(mk(1))\nmk(\"s\")",
	"err_struct_named":     "struct P { a, b }\nfn mk(c, v) { c(a: v) }\nprintln(mk(P, 1))",
	"err_struct_named_fn":  "fn f(a) { a }\nfn call(g) { g(a: 1) }\ncall(f)",
	"err_struct_field_set": "struct P { a: int }\nfn set(p, v) { let mut q = p\nq.a = v\nq }\nprintln(set(P(1), 2))\nset(P(1), nil)",
	"err_struct_set_int":   "fn set(v) { let mut q = v\nq.a = 1 }\nset(5)",
	"err_struct_arity":     "struct P { a }\nfn mk(f) { f(1, 2) }\nmk(P)",
	"err_struct_typed":     "struct P { a }\nstruct Q { a }\nfn f(p: P) { p }\nfn call(v) { f(v) }\ncall(Q(1))",
	"err_struct_json":      "import \"json\" as json\nstruct P { a }\njson.stringify(P(1))",
	"err_struct_add":       "struct P { a }\nfn add(v) { v + 1 }\nadd(P(1))",
	"element_types": `struct P { x: int }
let xs: array[int] = [1, 2, 3]
let m: map[string, array[int]] = {"a": xs, "b": []}
let ps: array[P] = [P(1), P(2)]
fn total(v: array[number]) -> number { let mut s = 0
for x in v { s = s + x }
s }
struct Bag { items: array[string] }
let mut bag = Bag([])
bag.items = ["x", "y"]
println(total(xs), total([1.5, 2]), m, ps, bag, total([]))
let nested: array[array[int]] = [[1], [], [2, 3]]
let anything: map[any, any] = {1: "a", "b": [2]}
println(nested, anything)
`,
	"err_elem_array":     "fn f(v) { let xs: array[int] = v\nxs }\nprintln(f([1, 2]))\nf([1, \"a\", nil])",
	"err_elem_nested":    "fn f(v: array[array[int]]) { v }\nfn call(v) { f(v) }\ncall([[1], [2, 3.5]])",
	"err_elem_map_value": "fn f(v: map[string, int]) { v }\nfn call(v) { f(v) }\ncall({\"z\": \"s\", \"a\": 1, \"b\": true})",
	"err_elem_map_key":   "fn f(v: map[string, int]) { v }\nfn call(v) { f(v) }\ncall({\"a\": 1, 2: 2})",
	"err_elem_return":    "fn f(v) -> array[string] { v }\nf([\"a\", 1])",
	"err_elem_struct":    "struct P { a }\nstruct Q { a }\nfn f(v) { let ps: array[P] = v\nps }\nf([P(1), Q(2)])",
	"err_elem_field":     "struct Bag { items: array[string] }\nfn mk(v) { Bag(v) }\nprintln(mk([]))\nmk([1])",
	"err_elem_assign":    "let mut xs: array[int] = []\nfn set(v) { xs = v }\nset([1])\nprintln(xs)\nset([\"no\"])",
	"mem": `import "mem" as mem
let b = mem.alloc(32)
println(b, type(b), mem.size(b), b == b, mem.get_i64(b, 0))
mem.set_i8(b, 0, -1)
mem.set_u8(b, 1, 255)
mem.set_i16(b, 2, -300)
mem.set_u16(b, 4, 65535)
mem.set_i32(b, 6, -2147483648)
mem.set_u32(b, 10, 4294967295)
mem.set_i64(b, 14, -9223372036854775807 - 1)
mem.set_f32(b, 22, 1.5)
println(mem.get_i8(b, 0), mem.get_u8(b, 0), mem.get_u8(b, 1), mem.get_i16(b, 2), mem.get_u16(b, 4))
println(mem.get_i32(b, 6), mem.get_u32(b, 10), mem.get_i64(b, 14), mem.get_f32(b, 22), mem.get_u16(b, 0))
mem.set_f64(b, 24, 0.1)
println(mem.get_f64(b, 24), mem.get_f32(b, 24))
mem.set_cstring(b, 0, "unda")
println(mem.get_cstring(b, 0), mem.get_cstring(b, 2), mem.get_u8(b, 4))
let empty = mem.alloc(0)
println(mem.size(empty), empty)
mem.free(b)
println(b)
`,
	"err_mem_bounds":    "import \"mem\" as mem\nlet b = mem.alloc(8)\nprintln(mem.get_i64(b, 0))\nmem.get_i32(b, 5)",
	"err_mem_negative":  "import \"mem\" as mem\nfn a(n) { mem.alloc(n) }\na(-1)",
	"err_mem_freed":     "import \"mem\" as mem\nlet b = mem.alloc(8)\nmem.free(b)\nmem.set_i8(b, 0, 1)",
	"err_mem_double":    "import \"mem\" as mem\nlet b = mem.alloc(8)\nmem.free(b)\nmem.free(b)",
	"err_mem_range":     "import \"mem\" as mem\nlet b = mem.alloc(8)\nmem.set_u8(b, 0, 255)\nmem.set_i8(b, 0, 128)",
	"err_mem_u64":       "import \"mem\" as mem\nlet b = mem.alloc(8)\nmem.set_i64(b, 0, -1)\nmem.get_u64(b, 0)",
	"err_mem_nul":       "import \"mem\" as mem\nlet b = mem.alloc(4)\nmem.set_u32(b, 0, 4294967295)\nmem.get_cstring(b, 1)",
	"err_mem_cstr_room": "import \"mem\" as mem\nlet b = mem.alloc(4)\nmem.set_cstring(b, 0, \"abc\")\nmem.set_cstring(b, 1, \"abc\")",
	"err_mem_value":     "import \"mem\" as mem\nfn s(v) { mem.set_f64(mem.alloc(8), 0, v) }\ns(\"x\")",
	"err_mem_offset":    "import \"mem\" as mem\nfn g(o) { mem.get_u8(mem.alloc(1), o) }\ng(nil)",
	"err_mem_arity":     "import \"mem\" as mem\nfn g(f) { f(mem.alloc(1)) }\ng(mem.get_u8)",
	"bits": `import "bits" as bits
println(bits.and(12, 10), bits.or(12, 10), bits.xor(12, 10), bits.not(0), bits.not(-1))
println(bits.shl(1, 62), bits.shl(1, 63), bits.shl(3, 63), bits.shr(-16, 2), bits.ushr(-16, 60), bits.shr(16, 63))
println(bits.count(0), bits.count(255), bits.count(-1), bits.and(-1, 9223372036854775807))
fn flag(n) { bits.shl(1, n) }
println(flag(0), flag(10), bits.or(flag(1), flag(3)))
`,
	"err_bits_shift":     "import \"bits\" as bits\nfn s(n) { bits.shl(1, n) }\nprintln(s(63))\ns(64)",
	"err_bits_arg":       "import \"bits\" as bits\nfn a(v) { bits.and(1, v) }\na(1.5)",
	"exit_code":          "import \"os\" as os\nprintln(\"before\")\nfn leave(n) { os.exit(n) }\nleave(7)\nprintln(\"after\")",
	"exit_in_callback":   "import \"os\" as os\nimport \"arrays\" as arrays\narrays.map([1, 2, 3], fn(n) { println(n)\nif n == 2 { os.exit(4) } })\nprintln(\"after\")",
	"exit_zero":          "import \"os\" as os\nwhile true { os.exit(0) }\nprintln(\"after\")",
	"err_exit_range":     "import \"os\" as os\nfn e(n) { os.exit(n) }\ne(256)",
	"err_unhandled":      "fn f() { err(\"boom\") }\nprintln(\"before\")\nlet v = f()?",
	"err_try_type":       "fn f(v) { v? }\nf(5)",
	"err_json_result":    "import \"json\" as json\njson.stringify([ok(1)])",
	"err_typed_let":      "fn g() { \"s\" }\nlet x: int = g()",
	"err_typed_assign":   "let mut n: int = 1\nfn set(v) { n = v }\nset(2)\nprintln(n)\nset(\"s\")",
	"err_typed_param":    "fn f(a: int) { a }\nfn call(v) { f(v) }\ncall(\"s\")",
	"err_typed_callback": "import \"arrays\" as arrays\narrays.map([1, \"x\"], fn(v: int) { let w = v\nw })",
	"err_typed_return":   "fn f(v) -> int {\n    if v { return v }\n    0\n}\nprintln(f(nil))\nf(\"s\")",
	"err_typed_implicit": "fn f(v) -> string {\n    let x = v\n    x\n}\nf(1)",
	"err_typed_no_else":  "fn f(v) -> int {\n    if v { 1 }\n}\nprintln(f(true))\nf(false)",
	"err_typed_pipeline": "fn up(s: string) { s }\nfn run(v) { v |> up }\nrun(1)",
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

// os needs a scratch directory, both backends see it through the
// environment
func TestNativeOS(t *testing.T) {
	cc := requireCC(t)
	dir := t.TempDir()
	t.Setenv("UNDA_TEST_DIR", filepath.Join(dir, "work"))
	if err := os.Mkdir(filepath.Join(dir, "work"), 0755); err != nil {
		t.Fatal(err)
	}
	src := `import "os" as os
import "process" as process
import "strings" as strings
let dir = process.env("UNDA_TEST_DIR")
let sub = dir + "/sub"
fn clean(e) { strings.replace(e, dir, "DIR") }
println(os.exists(sub), os.is_dir(dir), os.list_dir(dir))
os.mkdir(sub)
write_file(sub + "/b.txt", "x")
write_file(sub + "/a.txt", "y")
println(os.exists(sub), os.is_dir(sub), os.is_dir(sub + "/a.txt"), os.list_dir(sub))
match os.try_mkdir(sub) { ok(_) -> println("made"), err(e) -> println(clean(e)) }
match os.try_remove(sub) { ok(_) -> println("removed"), err(e) -> println(clean(e)) }
match os.try_list_dir(sub + "/a.txt") { ok(_) -> println("listed"), err(e) -> println(clean(e)) }
match os.try_list_dir(sub + "/nope") { ok(_) -> println("listed"), err(e) -> println(clean(e)) }
match os.try_remove(sub + "/nope") { ok(_) -> println("removed"), err(e) -> println(clean(e)) }
os.remove(sub + "/a.txt")
os.remove(sub + "/b.txt")
os.remove(sub)
println(os.exists(sub), type(os.getpid()), os.cwd() == os.cwd(), len(os.cwd()) > 0)
os.mkdir(sub + "/deeper/still")
`
	path := filepath.Join(dir, "os_test.unda")
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	compareBackends(t, cc, path)
}

func TestNativeExamples(t *testing.T) {
	cc := requireCC(t)
	for _, name := range []string{"phase0", "mutability", "phase1_data_pipeline", "phase3_types", "phase3_results", "phase4_structs", "phase4_system"} {
		t.Run(name, func(t *testing.T) {
			compareBackends(t, cc, filepath.Join("..", "examples", name+".unda"))
		})
	}
}

// gcStressThreshold makes native test binaries collect every few KiB. set
// UNDA_TEST_GC_THRESHOLD=1 to collect on every allocation
var gcStressThreshold = func() string {
	if v := os.Getenv("UNDA_TEST_GC_THRESHOLD"); v != "" {
		return v
	}
	return "4096"
}()

// a loop that allocates way more than it keeps has to run in bounded memory
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
	// collect very often so a missed GC root shows up as a mismatch
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

// extern functions only run natively, so this checks the output directly
func TestNativeExtern(t *testing.T) {
	cc := requireCC(t)
	dir := t.TempDir()
	lib := `#include <stdint.h>
#include <stdlib.h>
int64_t area(int32_t w, int32_t h) { return (int64_t)w * h; }
const char *greet(int ok) { return ok ? "hello from C" : 0; }
static int counter;
void bump(void) { counter++; }
int32_t count(void) { return counter; }
uint64_t huge(void) { return UINT64_MAX; }
typedef struct { int n; } Counter;
void *counter_new(void) { return calloc(1, sizeof(Counter)); }
void counter_inc(void *c) { ((Counter *)c)->n++; }
int32_t counter_get(void *c) { return ((Counter *)c)->n; }
void counter_free(void *c) { free(c); }
void *nothing(void) { return 0; }
typedef struct { double x, y; } Vec2;
typedef struct { Vec2 min, max; const char *name; int32_t id; } Box;
Vec2 vec_scale(Vec2 v, double by) { Vec2 r = {v.x * by, v.y * by}; return r; }
Box box_grow(Box b, double by) { b.max = vec_scale(b.max, by); b.id++; return b; }
double box_area(Box b) { return (b.max.x - b.min.x) * (b.max.y - b.min.y); }
int name_set(Box b) { return b.name != 0; }
`
	if err := os.WriteFile(filepath.Join(dir, "geo.c"), []byte(lib), 0644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(cc, "-shared", "-fPIC", "-o", filepath.Join(dir, "libgeo.so"), filepath.Join(dir, "geo.c")).CombinedOutput(); err != nil {
		t.Fatalf("building test library: %v\n%s", err, out)
	}
	tests := []struct {
		name, src, stdout, stderr string
	}{
		{"calls", `extern "m" { fn cos(x: f64) -> f64; fn pow(a: f64, b: f64) -> f64 }
extern "c" {
    fn labs(n: i64) -> i64
    fn strlen(s: cstring) -> u64
    fn getenv(name: cstring) -> cstring
}
extern "./libgeo.so" {
    fn area(w: i32, h: i32) -> i64
    fn greet(ok: i32) -> cstring
    fn bump()
    fn count() -> i32
}
println(cos(0), pow(2, 10), labs(-42), strlen("abc"), getenv("UNDA_NO_SUCH_VAR"))
println(area(3, 4), greet(1), greet(0), bump(), count(), type(cos))
`, "1 1024 42 3 nil\n12 hello from C nil nil 1 builtin\n", ""},
		{"range", `extern "./libgeo.so" { fn area(w: i32, h: i32) -> i64 }
fn big(n) { area(n, 1) }
big(5000000000)`, "", "area argument 1 is out of range for i32: 5000000000"},
		{"negative unsigned", `extern "c" { fn malloc(n: u64) -> u64 }
fn m(n) { malloc(n) }
m(-1)`, "", "malloc argument 1 is out of range for u64: -1"},
		{"arg type", `extern "m" { fn cos(x: f64) -> f64 }
fn c(v) { cos(v) }
c("x")`, "", "cos argument 1 must be number, got STRING"},
		{"nul byte", `import "json" as json
extern "c" { fn strlen(s: cstring) -> u64 }
fn l(v) { strlen(v) }
l("a" + json.parse("\"\\u0000\""))`, "", "strlen argument 1 contains a NUL byte"},
		{"handles", `import "process" as process
extern "./libgeo.so" {
    fn counter_new() -> ptr
    fn counter_inc(c: ptr)
    fn counter_get(c: ptr) -> i32
    fn counter_free(c: ptr)
    fn nothing() -> ptr
}
extern "c" {
    fn fopen(path: cstring, mode: cstring) -> ptr
    fn fputs(s: cstring, f: ptr) -> i32
    fn fclose(f: ptr) -> i32
}
let c = counter_new()
counter_inc(c)
counter_inc(c)
fn get(h: ptr) -> int { counter_get(h) }
println(get(c), type(c), c == c, c == nothing(), nothing(), fopen("/nonexistent/unda", "r"))
counter_free(c)
let path = process.env("UNDA_FFI_OUT")
let f = fopen(path, "w")
fputs("written from unda", f)
fclose(f)
println(read_file(path))`, "2 pointer true false nil nil\nwritten from unda\n", ""},
		{"ptr arg type", `extern "./libgeo.so" { fn counter_get(c: ptr) -> i32 }
fn g(v) { counter_get(v) }
g(5)`, "", "counter_get argument 1 must be POINTER, got INTEGER"},
		{"c structs", `extern "./libgeo.so" {
    struct Vec2 { x: f64, y: f64 }
    struct Box {
        min: Vec2
        max: Vec2
        name: cstring
        id: i32
    }
    fn vec_scale(v: Vec2, by: f64) -> Vec2
    fn box_grow(b: Box, by: f64) -> Box
    fn box_area(b: Box) -> f64
    fn name_set(b: Box) -> i32
}
let b = Box(Vec2(0, 0), Vec2(2, 3), "room", 7)
let g = box_grow(b, 2)
println(vec_scale(Vec2(1, 2), 3), g, box_area(b), box_area(g))
println(name_set(b), name_set(Box(Vec2(0, 0), Vec2(0, 0), nil, 0)), type(g), g == box_grow(b, 2))
let mut moved = b
moved.min.x = -1
println(box_area(moved))
`, "Vec2(x: 3, y: 6) Box(min: Vec2(x: 0, y: 0), max: Vec2(x: 4, y: 6), name: room, id: 8) 6 24\n1 0 box true\n9\n", ""},
		{"c struct field range", `extern "./libgeo.so" {
    struct Box { min: Vec2, max: Vec2, name: cstring, id: i32 }
    struct Vec2 { x: f64, y: f64 }
    fn box_area(b: Box) -> f64
}
fn area(id) { box_area(Box(Vec2(0, 0), Vec2(1, 1), "x", id)) }
println(area(1))
area(5000000000)`, "1\n", "box_area argument 1 field id is out of range for i32: 5000000000"},
		{"c struct wrong struct", `extern "./libgeo.so" {
    struct Vec2 { x: f64, y: f64 }
    fn vec_scale(v: Vec2, by: f64) -> Vec2
}
struct Other { x, y }
fn s(v) { vec_scale(v, 1) }
s(Other(1, 2))`, "", "vec_scale argument 1 must be Vec2, got Other"},
		{"u64 result", `extern "./libgeo.so" { fn huge() -> u64 }
huge()`, "", "huge returned 18446744073709551615, too big for an int"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(dir, tt.name+".unda")
			if err := os.WriteFile(path, []byte(tt.src), 0644); err != nil {
				t.Fatal(err)
			}
			exe := filepath.Join(dir, tt.name)
			if err := compiler.Build(path, compiler.Options{Output: exe, CC: cc}); err != nil {
				var diagErr *compiler.DiagnosticsError
				if errors.As(err, &diagErr) {
					t.Fatalf("diagnostics: %v", diagErr.Program.Diagnostics)
				}
				t.Fatal(err)
			}
			var out, errOut bytes.Buffer
			cmd := exec.Command(exe)
			cmd.Env = append(os.Environ(), "UNDA_FFI_OUT="+filepath.Join(dir, "ffi_out.txt"))
			cmd.Stdout, cmd.Stderr = &out, &errOut
			_ = cmd.Run()
			if out.String() != tt.stdout || !strings.Contains(errOut.String(), tt.stderr) {
				t.Fatalf("got stdout %q stderr %q", out.String(), errOut.String())
			}
		})
	}

	var out, errOut bytes.Buffer
	path := filepath.Join(dir, "calls.unda")
	if code := runner.RunFile(path, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "extern functions need the native backend") {
		t.Fatalf("interpreter should refuse extern, got exit %d stderr %q", code, errOut.String())
	}
}
