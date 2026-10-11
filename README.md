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
- `read_file(path)`, `write_file(path, content)`, `append_file(path, content)`

Standard modules:

- `strings`: `split`, `join`, `upper`, `lower`, `trim`, `replace`, `contains`, `has_prefix`, `has_suffix`
- `arrays`: `append`, `first`, `last`, `map`, `filter`, `reduce`, `range`, `reverse`, `slice`, `join`
- `maps`: `get`, `has`, `set`, `merge`, `keys`, `values`
- `json`: `parse`, `stringify`
- `math`: `abs`, `ceil`, `floor`, `max`, `min`, `pow`, `round`, `sqrt`
- `time`: `now`, `unix`, `format`
- `process`: `args`, `env`, `run`
- `shell`: `run`

## Examples

- `examples/phase0.unda`: foundation syntax
- `examples/mutability.unda`: mutable and immutable bindings
- `examples/phase1_data_pipeline.unda`: arrays, maps, match, pipeline, JSON
- `examples/phase1_import_fileio.unda`: local module import and file I/O
