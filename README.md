# undalang

Unda is a general-purpose language designed to feel simple and readable like Python at the start, while growing toward stronger low-level control over time.

The project roadmap and language design notes are in [ROADMAP.md](./ROADMAP.md).

## Run

```sh
go run ./cmd/unda repl
go run ./cmd/unda run examples/phase1_data_pipeline.unda
```

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

Unda supports arrays, maps, indexing, `if`, and `match`.

```unda
let xs = [1, 2, 3]
let data = {"name": "unda", "count": len(xs)}

match data["count"] {
    0 -> "empty",
    3 -> data["name"],
    _ -> "other",
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

- `examples/phase0.unda`: foundation syntax
- `examples/mutability.unda`: mutable and immutable bindings
- `examples/phase1_data_pipeline.unda`: arrays, maps, match, pipeline, JSON
- `examples/phase1_import_fileio.unda`: local module import and file I/O
