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

### Phase 0 — Foundation (bootstrapping)
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

### Phase 1 — Practical Scripting Language
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

### Phase 2 — Compiled Core (C backend)
Goal: transition from interpreter-first to production-style compiled output.

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

### Phase 3 — Stronger Type System and Performance
Goal: improve safety and correctness while preserving readability.

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

Potential deliverables:
- structs and user-defined data layouts
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
