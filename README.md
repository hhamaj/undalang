# ranalang

Rana is a general-purpose language designed to feel simple and readable like Python at the start, while growing toward stronger low-level control over time.

The project roadmap and language design notes are in [ROADMAP.md](./ROADMAP.md).

## Run

```sh
go run ./cmd/rana repl
go run ./cmd/rana run examples/phase1_data_pipeline.rana
```

## Current Syntax

Variables are immutable by default. Use `let mut` when a binding must be reassigned.

```rana
let name = "rana"
let mut count = 1
count = count + 1
```

Functions can be anonymous or named. Named functions are bindings in the current scope.

```rana
fn add(a, b) {
    return a + b
}

let double = fn(x) { return x * 2 }
```

Rana supports arrays, maps, indexing, `if`, and `match`.

```rana
let xs = [1, 2, 3]
let data = {"name": "rana", "count": len(xs)}

match data["count"] {
    0 -> "empty",
    3 -> data["name"],
    _ -> "other",
}
```

The pipeline operator calls the expression on the right with the left value inserted as the first argument. If the right side is a bare function, it is called with one argument.

```rana
import "strings" as strings
import "arrays" as arrays

fn shout(s) { return s |> strings.upper }

"rana,go"
    |> strings.split(",")
    |> arrays.map(shout)
    |> strings.join("-")
    |> println
```

## Modules

Use `import "module" as name` for standard modules or local `.rana` files. Local imports are resolved relative to the file being run; the `.rana` suffix is optional.

```rana
import "json" as json
import "stats" as stats

println(json.stringify(stats.describe([1, 2, 3])))
```

Imported Rana files expose their top-level bindings through member access:

```rana
import "stats" as stats
stats.sum([1, 2, 3])
```

## Builtins

Global builtins:

- `print(...)`, `println(...)`
- `len(value)` for strings, arrays, and maps
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

- `examples/phase0.rana`: foundation syntax
- `examples/mutability.rana`: mutable and immutable bindings
- `examples/phase1_data_pipeline.rana`: arrays, maps, match, pipeline, JSON
- `examples/phase1_import_fileio.rana`: local module import and file I/O
