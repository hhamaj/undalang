# undalang

Unda is a general-purpose language designed to feel simple and readable like Python at the start, while growing toward stronger low-level control over time.

The project roadmap and language design notes are in [ROADMAP.md](./ROADMAP.md).

## Run

```sh
go run ./cmd/unda repl
go run ./cmd/unda run examples/phase1_data_pipeline.unda          # interpreter
go run ./cmd/unda check examples/phase1_data_pipeline.unda        # static checks only
go run ./cmd/unda build examples/phase1_data_pipeline.unda -o app # native executable
go run ./cmd/unda run --native examples/phase1_data_pipeline.unda # build + run
```

`unda build` needs a C compiler (`cc`, or `$CC`). Pass `--emit-c out.c` to keep the generated C.

## Checking and Diagnostics

`unda check` resolves every name and runs a conservative type check without executing the program. It reports undefined identifiers, assignments to immutable bindings, operator type mismatches between statically known values (`len(x) + "!"`), calls to non-functions, wrong argument counts for known functions and builtins, unknown module members, and invalid map keys or index types. Code whose types depend on runtime values (function parameters, `let mut` bindings) is accepted as is.

```text
main.unda:3:19: error: type mismatch: INTEGER + STRING
  3 | let n = len(name) + "!"
    |                   ^
```

Runtime errors carry the same location in both the interpreter and native executables:

```text
runtime error: main.unda:6:20: type mismatch: INTEGER + STRING
```

## Native Backend

`unda build` lowers the program to C and compiles it with the system C compiler. The native executable behaves like the interpreter: same output formatting, same error messages and locations, and the same standard library. The test suite enforces this by running every program through both backends and comparing stdout, stderr, and the exit code.

The generated code does integer arithmetic and comparisons inline and calls functions declared with `fn` directly, falling back to the general (and error-reporting) path only when it has to, so it runs well ahead of the interpreter: about 9x faster than Python 3.14 on recursive calls and 45x on integer loops in our measurements.

Native executables free unused memory with a conservative mark-and-sweep garbage collector. It runs once the program has allocated more than its live heap (at least 8 MiB); set `UNDA_GC_THRESHOLD=<bytes>` to change the minimum.

Current limitations:

- `time.format` is not available natively yet (`unda build` reports it at compile time).
- `process.args()` returns the executable's own arguments.
- Recursion is limited to 10000 nested calls in both backends.

## Current Syntax

Variables are immutable by default. Use `let mut` when a binding must be reassigned.

```unda
let name = "unda"
let mut count = 1
count = count + 1
```

Functions can be anonymous or named. Named functions are bindings in the current scope.

```unda
fn add(a, b) {
    return a + b
}

let double = fn(x) { return x * 2 }
```

Comments start with `//` and run to the end of the line.

`m.key` reads a string key from a map, like `m["key"]`; missing keys give `nil`.

Unda supports arrays, maps, indexing, `if`, and `match`. Strings are indexed by character: `len("çay")` is 3 and `"çay"[0]` is `"ç"`.

```unda
let xs = [1, 2, 3]
let data = {"name": "unda", "count": len(xs)}

match data["count"] {
    0 -> "empty",
    3 -> data["name"],
    _ -> "other",
}
```

`match` arms take patterns. A bare name binds the value, `[first, ..rest]` destructures arrays, `{"key": value}` destructures maps (extra keys are allowed), and `if` adds a guard. Literals compare by value; to compare against a variable, use a guard such as `x if x == limit`.

```unda
fn describe(v) {
    match v {
        [] -> "empty",
        [first, ..rest] -> "starts with " + type(first),
        {"name": name} -> name,
        n if type(n) == "integer" && n < 0 -> "negative",
        _ -> "other",
    }
}
```

## Type Annotations

Types are optional. Bindings, parameters and return values can carry one, untyped code works exactly as before.

```unda
let mut count: int = 0

fn add(a: int, b: int) -> int {
    return a + b
}

let label = fn(name: string, value) -> string { name + ": " + type(value) }
```

The types are `int`, `float`, `number` (int or float), `string`, `bool`, `nil`, `array`, `map`, `fn` (functions and builtins), `module`, `result`, `ptr`, `buffer`, `any` and the name of any struct.

Arrays and maps can say what they hold: `array[int]`, `map[string, int]`, `array[Point]`, `map[string, array[float]]`. Map keys can be `int`, `string`, `bool`, `nil` or `any`. Checking one of these at runtime looks at every element, so it costs time proportional to the collection; a mismatch names the first offender:

```text
runtime error: main.unda:4:1: type mismatch: scores expects map[string, int], got MAP (value at key "bob" is STRING)
```

`unda check` reports mismatches it can prove without running the program, and typed bindings and parameters give it more to work with:

```text
main.unda:1:28: error: type mismatch: f returns string, got INTEGER
  1 | fn f(a: int) -> string { a + 1 }
    |                            ^
```

Everything else is checked at runtime, in both backends: `let` and assignments to typed bindings, arguments to typed parameters, and the value a typed function returns (whether through `return` or by falling off the end of its body).

```text
runtime error: main.unda:3:1: type mismatch: parameter a expects int, got STRING
```

A name redeclared in the same scope has to keep its type.

## Errors as Values

Errors you expect are values: `ok(value)` or `err(reason)`. `match` takes them apart, and `?` after an expression unwraps an `ok` or returns the `err` from the current function right away.

```unda
fn parse_port(s: string) -> result {
    match s {
        "80" -> ok(80),
        _ -> err("not a port: " + s),
    }
}

fn parse_addr(host: string, port: string) -> result {
    let p = parse_port(port)?
    ok({"host": host, "port": p})
}

match parse_addr("localhost", "x") {
    ok(a) -> println(a.host, a.port),
    err(e) -> println("error:", e),
}
```

Builtins that can fail because of the outside world (a missing file, broken JSON, a command exiting non-zero) have a `try_` twin that returns `ok(value)` or `err(message)` instead of stopping the program. Passing the wrong kind of argument is still a runtime error, since that's a bug in the program rather than something to handle.

```unda
import "json" as json

fn load_config(path: string) -> result {
    let text = try_read_file(path)?
    json.try_parse(text)
}
```

`?` applies to the expression right before it, so `f(x)? + 1` unwraps `f(x)` but `x |> f?` unwraps `f`; write `(x |> f)?` to unwrap a pipeline. Using `?` outside a function, where there is nothing to return to, makes an `err` a runtime error (`unhandled err(...)`). A function with a return type other than `result` or `any` can't use `?`, and `unda check` says so.

## Structs

A struct groups named fields. Field types are optional, like everywhere else. Build a value by calling the struct with the fields in order, or by name:

```unda
struct Point {
    x: number
    y: number
}

let p = Point(1, 2)
let q = Point(y: 4, x: 3)
println(p.x, q)             // 1 Point(x: 3, y: 4)

fn dist2(a: Point, b: Point) -> number {
    (a.x - b.x) * (a.x - b.x) + (a.y - b.y) * (a.y - b.y)
}
```

Struct names start with an uppercase letter and work as types (`p: Point`). Structs are values: `r.x = 5` on a `let mut` binding gives `r` a copy with that field changed, and anything else holding the old value keeps it. The same works for string keys of maps (`cfg.db.port = 5432`). Two struct values are equal when they come from the same declaration and their fields are equal.

## Calling C

`extern` declares functions from a C library with their C types. After that they are called like any builtin:

```unda
extern "m" {
    fn sqrt(x: f64) -> f64
    fn pow(base: f64, exp: f64) -> f64
}

extern "./libgeo.so" {
    fn area(w: i32, h: i32) -> i64
    fn reset()
}

println(sqrt(pow(3, 2) + pow(4, 2)), area(3, 4))
```

The C types are `i8`, `i16`, `i32`, `i64`, `u8`, `u16`, `u32`, `u64`, `f32`, `f64`, `bool`, `cstring` (a NUL-terminated `char *`; a NULL result comes back as `nil`) and `ptr`. Leave out the return type for `void`.

`ptr` is any other pointer, like a `FILE *` or a handle a library gives out. Unda code can only hold it, compare it and pass it back to C; it can't read through it or do arithmetic on it. NULL comes back as `nil`, and `nil` passed as a `ptr` is NULL. `ptr` also works as an ordinary type annotation.

```unda
extern "c" {
    fn fopen(path: cstring, mode: cstring) -> ptr
    fn fputs(s: cstring, f: ptr) -> i32
    fn fclose(f: ptr) -> i32
}

let f = fopen("/tmp/out.txt", "w")
if f == nil { println("could not open") } else {
    fputs("hello from unda\n", f)
    fclose(f)
}
``` Arguments are converted when the call happens and must fit: an int that doesn't fit an `i32` or a negative int for a `u64` is a runtime error, not a silent truncation.

A struct declared inside an extern block gets a C layout, fields in order with C types, and can go to and come back from C by value. On the Unda side it's an ordinary struct (an `i32` field takes an int, an `f64` field a number):

```unda
extern "./libgeo.so" {
    struct Vec2 { x: f64, y: f64 }
    struct Box { min: Vec2, max: Vec2, name: cstring, id: i32 }
    fn vec_scale(v: Vec2, by: f64) -> Vec2
    fn box_area(b: Box) -> f64
}

println(vec_scale(Vec2(1, 2), 3))    // Vec2(x: 3, y: 6)
```

Only structs declared this way can cross into C; `cstring` and `ptr` fields may be `nil` (NULL).

The library is `"c"` for libc, a name like `"m"` or `"sqlite3"` to link `-lm` / `-lsqlite3`, or a path to a `.so` or `.a` file (relative to the source file). Shared libraries given by path are found again at runtime through an rpath.

### Buffers

Some C functions want memory to fill in, like `gettimeofday` filling a `struct timeval`. The `mem` module hands out buffers of C memory for that. A buffer passes wherever an extern function takes a `ptr`, and its bytes are read and written at byte offsets:

```unda
import "mem" as mem

extern "c" { fn gettimeofday(tv: ptr, tz: ptr) -> i32 }

let tv = mem.alloc(16)               // zeroed, freed only by mem.free
gettimeofday(tv, nil)
println(mem.get_i64(tv, 0))          // seconds
mem.free(tv)
```

There are `get_` and `set_` functions for `i8` through `u64`, `f32`, `f64` and `cstring`, plus `alloc`, `free` and `size`. Every access is bounds checked, values have to fit the type (`set_i8` of 300 is an error) and using a buffer after `mem.free` is a runtime error, in Unda code and when handing it to C. Numbers use the machine's byte order. What C does with the pointer is up to C.

Buffers are the one thing in Unda that changes in place: two names for the same buffer see the same bytes. That's the point of them, they stand for memory that C shares. The `mem` module works in both backends; only passing buffers to C needs the native one.

Extern functions only work in the native backend (`unda build`, `unda run --native`); the interpreter stops at the `extern` block with an error. Declaring a function with the wrong C signature is undefined behavior, same as in C.

## Loops

`while` repeats while its condition is truthy. `for` iterates arrays (elements), maps (keys in sorted order) and strings (characters); with two names it also binds the index or key. `break` and `continue` work in both. Each iteration gets fresh bindings, so closures created in a loop capture that iteration's values.

```unda
let mut i = 0
while i < 3 {
    i = i + 1
}

for name in ["ada", "bo"] { println(name) }
for i, name in ["ada", "bo"] { println(i, name) }
for key, value in {"a": 1, "b": 2} {
    if value == 2 { break }
    println(key, value)
}
```

The pipeline operator calls the expression on the right with the left value inserted as the first argument. If the right side is a bare function, it is called with one argument.

```unda
import "strings" as strings
import "arrays" as arrays

fn shout(s) { return s |> strings.upper }

"unda,go"
    |> strings.split(",")
    |> arrays.map(shout)
    |> strings.join("-")
    |> println
```

## Modules

Use `import "module" as name` for standard modules or local `.unda` files. Local imports are resolved relative to the file being run; the `.unda` suffix is optional.

```unda
import "json" as json
import "stats" as stats

println(json.stringify(stats.describe([1, 2, 3])))
```

Imported Unda files expose their top-level bindings through member access:

```unda
import "stats" as stats
stats.sum([1, 2, 3])
```

## Builtins

Global builtins:

- `print(...)`, `println(...)`
- `len(value)` for strings (characters), arrays, and maps
- `type(value)`
- `ok(value)`, `err(reason)`
- `read_file(path)`, `write_file(path, content)`, `append_file(path, content)`
- `try_read_file`, `try_write_file`, `try_append_file`: same arguments, return a result

Standard modules:

- `strings`: `split`, `join`, `upper`, `lower`, `trim`, `replace`, `contains`, `has_prefix`, `has_suffix`
- `arrays`: `append`, `first`, `last`, `map`, `filter`, `reduce`, `range`, `reverse`, `slice`, `join`
- `maps`: `get`, `has`, `set`, `merge`, `keys`, `values`
- `json`: `parse`, `try_parse`, `stringify`
- `math`: `abs`, `ceil`, `floor`, `max`, `min`, `pow`, `round`, `sqrt`
- `time`: `now`, `unix`, `format`
- `process`: `args`, `env`, `run`, `try_run`
- `mem`: `alloc`, `free`, `size`, `get_`/`set_` for `i8`…`u64`, `f32`, `f64`, `cstring`
- `shell`: `run`, `try_run`

## Examples

- `examples/phase0.unda`: foundation syntax
- `examples/mutability.unda`: mutable and immutable bindings
- `examples/phase1_data_pipeline.unda`: arrays, maps, match, pipeline, JSON
- `examples/phase1_import_fileio.unda`: local module import and file I/O
- `examples/phase3_types.unda`: type annotations
- `examples/phase3_results.unda`: `ok`/`err` results and `?`
- `examples/phase4_structs.unda`: structs
- `examples/phase4_ffi.unda`: calling C functions (native only)
