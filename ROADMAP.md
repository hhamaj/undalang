# Undalang Roadmap and Design Direction

## 1. Introduction

**Undalang** is a general-purpose programming language focused on one idea:

**Easy to start with, powerful when you need control.**

It should feel clean and readable for beginners, while leaving room for advanced capabilities such as typed functions, structs, memory control, pointers, and C interoperability as the language matures.

Example beginner-style code:

```unda
let name = "Mehmet Hamza"
print("Hello", name)
```

Unda is meant to begin as a practical language for:
- scripts and automation
- file/data processing
- small CLI tools
- simple app logic and game scripting

---

## 2. Core Philosophy

1. **Readable first**: syntax should stay clear and concise.
2. **Practical minimalism**: small, useful feature set before complexity.
3. **Progressive power**: advanced control should be added in layers.
4. **No “toy language” trap**: early versions should already solve real problems.

---

## 3. Language Highlights

### Pipeline operator

Data-flow should stay readable:

```unda
"hello world"
    |> split(" ")
    |> map(upper)
    |> join("-")
    |> print
```

### Pattern matching

```unda
match value {
    0 -> print("zero")
    1 -> print("one")
    _ -> print("something else")
}
```

### Safe, explicit error handling

Unda should favor explicit, composable error handling (Result/`?`-style propagation) rather than hidden exception-heavy flows or unsafe manual patterns.

---

## 4. Implementation Language and Build Model

Undalang implementation should be written in **Go (Golang)**.

The first stable compiled strategy should target native executables through a **C backend**:

`.unda source`  
`→ lexer`  
`→ parser`  
`→ AST`  
`→ semantic analysis`  
`→ type checking`  
`→ C code generation`  
`→ GCC/Clang`  
`→ native executable`

Generated C is an internal backend target, not the user-facing language.

Planned future backends:
- LLVM IR backend
- direct native code backend
- bytecode VM backend (for scripting/embedding)

---

## 5. Development Roadmap

### Phase 0 — Foundation (bootstrapping) ✅
Goal: build the minimum language toolchain and execution loop.

Deliverables:
- lexer
- parser
- AST definitions
- evaluator/interpreter
- scoped environment
- core built-in functions
- REPL
- file execution (`unda run file.unda`)

Success criteria:
- simple expressions/statements run correctly
- variables and basic control flow work
- REPL and script mode are stable enough for daily language development

---

### Phase 1 — Practical Scripting Language ✅
Goal: make Unda useful for real small tools/scripts.

Deliverables:
- user-defined functions
- arrays/lists
- maps/dictionaries
- match expressions
- pipeline operator (`|>`)
- module/import system (basic)
- file I/O primitives
- standard library v1:
  - strings
  - arrays
  - maps
  - JSON
  - math
  - time
  - process/shell utilities

Success criteria:
- users can write non-trivial automation and data-processing scripts
- module boundaries and package layout are clear and predictable

---

### Phase 2 — Compiled Core (C backend) ✅ (foundation)
Goal: transition from interpreter-first to production-style compiled output.

Status:
- `semantic` package: symbol resolution, scope validation, and a kind-based type check (`unda check`)
- `compiler` package: C code generator plus an embedded C runtime (`unda build`, `unda run --native`)
- `diag` package: `file:line:col` diagnostics with source excerpts; runtime errors carry positions in both backends
- differential tests keep the interpreter and native output identical
- conservative mark-and-sweep garbage collector in the C runtime
- before phase 3: `//` comments, `while`/`for` loops with `break`/`continue`, destructuring `match` patterns with guards, character-based strings
- open items: `time.format` in the C runtime

Deliverables:
- semantic analysis pass
- symbol resolution and scope validation
- type checking foundations
- C code generator
- compiler CLI (`unda build`, `unda run`)
- GCC/Clang integration for native binaries
- source span mapping for diagnostics

Success criteria:
- compiled programs run correctly on major platforms supported by Go + C toolchains
- diagnostics include clear file/line context
- generated C remains internal and replaceable

---

### Phase 3 — Stronger Type System and Performance ✅
Goal: improve safety and correctness while preserving readability.

Status:
- optional type annotations on `let`, parameters and return types (`fn add(a: int, b: int) -> int`), checked statically where provable and at runtime in both backends
- the checker uses annotations as type information, so typed code gets more static errors
- `ok(v)`/`err(e)` result values, `ok(...)`/`err(...)` match patterns and the `?` operator, in both backends
- `try_` versions of the builtins that can fail at runtime (`try_read_file`, `try_write_file`, `try_append_file`, `json.try_parse`, `process.try_run`, `shell.try_run`)
- fixed a native GC bug where an inlined program body could keep values above the scanned stack range
- C backend optimizations: integer arithmetic and comparisons inline with a tag check (the generic path only runs, and only sets the error location, when the operands aren't both ints), direct calls to functions bound once with `fn`, and folding of constant integer expressions
- interpreter optimizations: small slice-based scopes instead of maps, no scope for blocks that declare nothing, shared small integers
- diagnostics suggest close matches for unknown names, module members, type names and standard modules (`did you mean "upper"?`)
- deferred to Phase 4: element types such as `array[int]`. checking them at runtime means walking the whole collection on every typed assignment, and they belong with user-defined types and generics

Benchmark results (fib(30), counting primes below 200000, map/filter/reduce over 1M ints), compared with the start of the phase:
- native: 36 → 11 ms, 141 → 20 ms, 57 → 55 ms (C: 2, 13, 11 ms; Python 3.14: 98, 936, 140 ms)
- interpreter: 1668 → 706 ms, 3258 → 1556 ms, 899 → 431 ms

Deliverables:
- optional type annotations
- richer type checker
- typed functions and return types
- improved error propagation model (`Result`/`?` style)
- optimization passes (early and conservative)
- improved compiler diagnostics and tooling

Success criteria:
- typed and untyped styles can coexist with clear rules
- common classes of runtime errors are caught at compile time

---

### Phase 4 — Systems-Level Capabilities
Goal: progressively add low-level control for advanced users.

Status:
- structs: `struct Point { x: number, y: number }`, positional and named construction, field access, struct names as types, field assignment that copies (`p.x = 1`, also for map keys), checked statically and at runtime in both backends
- C FFI: `extern "lib" { fn name(a: i32) -> f64 }` with integer, float, bool and cstring types, checked conversions, linking by library name or path. native backend only for now; the interpreter would need cgo or a dlopen library such as purego
- opaque pointers: `ptr` in extern signatures and annotations for handles C gives out (`FILE *`, library objects). Unda code holds, compares and passes them back, nothing more; NULL is nil. this is the deliberately narrow pointer story: Unda values stay values, raw memory stays on the C side
- next: element types, C struct layouts over FFI, a small module for C-owned buffers

Potential deliverables:
- structs and user-defined data layouts
- element types for collections (`array[int]`, `map[string, int]`), designed together with user-defined types
- pointers/references
- manual memory-control primitives (carefully scoped)
- C FFI
- low-level modules for systems integration

Success criteria:
- advanced features are opt-in and do not harm beginner experience
- interoperability and control features are reliable and well-documented

---

### Phase 5 — Backend Expansion
Goal: make compilation architecture backend-agnostic.

Deliverables:
- backend abstraction layer in compiler architecture
- optional LLVM backend
- optional direct native code backend
- optional bytecode VM backend for embedding and fast script startup

Success criteria:
- front-end and semantic phases are shared cleanly across backends
- backend choice can be configured without language-level fragmentation

---

## 6. CLI and Tooling Direction

Initial command surface:
- `unda repl`
- `unda run <file.unda>`
- `unda build <file.unda> [-o output]`
- `unda fmt <path>`
- `unda check <file.unda>`

Tooling priorities:
1. clear error messages with source locations
2. deterministic formatting
3. consistent package/module layout
4. simple project initialization workflow

---

## 7. Non-Goals (Early Stages)

To keep scope realistic, early versions should avoid:
- building every advanced feature at once
- over-designing generics/macros before core stability
- exposing generated C as public API surface
- sacrificing readability for premature micro-optimizations

---

## 8. Design Guardrails

- Keep syntax small and teachable.
- Every new feature must improve real developer workflows.
- Add power incrementally, not all at once.
- Protect beginner ergonomics while enabling expert control.

Unda should evolve from a clean scripting language into a serious systems-capable language without losing its simplicity.
