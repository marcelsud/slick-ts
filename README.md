# Slick

Slick is a Go command-line interface for normal TypeScript projects. The
TypeScript Compiler API is the source of truth. TypeScript owns parsing,
module resolution, and type checking.

Slick exposes machine-readable operational contracts for errors, authority
use, asynchronous ownership, and unresolved dependencies without custom
TypeScript syntax or a new runtime.

## Status

`slick check`, `slick describe`, `slick build`, and `slick crap` are available.
Commands return human or deterministic versioned JSON output.

The analyzer also creates internal operational summaries for authored and package source.
Each summary contains:

- Concrete errors and authority effects
- Synchronous or asynchronous execution mode
- Source provenance
- Unresolved calls

`slick describe` exposes the same resolved signatures and operational summaries
as a deterministic version 1 contract.

## Requirements

- Go 1.24 or newer
- Node.js 20 or newer
- TypeScript 5.9.3 in the checked project

Slick requires the exact TypeScript version because compiler behavior is part
of its semantic contract.

## Build

```sh
git clone https://github.com/marcelsud/slick-ts.git
cd slick-ts
npm ci
go build -o slick ./cmd/slick
```

The command creates the `slick` executable in the repository root.

## Check a project

Run Slick from a TypeScript project.

```sh
./slick check
```

Pass a path when the project is in another location.

```sh
./slick check path/to/project
./slick check path/to/project/src/main.ts
./slick check path/to/project/tsconfig.json
```

The path can identify a directory, a source file, or a `tsconfig.json` file.
Slick searches the path and its parent directories for the applicable
`tsconfig.json`.

Slick loads the configured project, runs TypeScript parsing, module resolution,
and type checking, then rejects unsafe `any`, unchecked assertions, implicit
truthiness, and unconsumed Promises. Slick does not emit JavaScript.

### Human output

Human output preserves TypeScript diagnostics and renders Slick diagnostics with
their semantic fact and concrete repair strategies.

```text
src/main.ts:1:7 - error TS2322: Type 'number' is not assignable to type 'string'.
```

### JSON output

Use `--json` for deterministic machine-readable output.

```sh
./slick check --json path/to/project
```

The command returns a version 1 document.

```json
{
  "version": 1,
  "command": "check",
  "success": false,
  "project": "tsconfig.json",
  "diagnostics": [
    {
      "source": "typescript",
      "code": 2322,
      "category": "error",
      "message": "Type 'number' is not assignable to type 'string'.",
      "path": "src/main.ts",
      "range": {
        "start": { "line": 1, "column": 7, "offset": 6 },
        "end": { "line": 1, "column": 12, "offset": 11 }
      }
    }
  ]
}
```

Paths use forward slashes and are relative to the project. Lines and columns
start at 1. Offsets start at 0. An unchanged project has stable diagnostic
order.

### Strict checks

Slick emits stable error codes for its initial rules.

| Code | Rule |
| ---: | --- |
| `SLICK1001` | Unsafe explicit or authored-flow `any` |
| `SLICK1002` | Unchecked type or non-null assertion |
| `SLICK1003` | Non-boolean condition or logical operand |
| `SLICK1004` | Promise not awaited, returned, joined, or transferred |

JSON diagnostics include the title, explanation, exact range, triggering fact,
and repairs. TypeScript declaration internals do not produce Slick diagnostics
until their values reach authored code.

## Check change risk

```sh
./slick crap --coverage coverage/coverage-final.json --threshold 30 [path]
./slick crap --json --coverage coverage/coverage-final.json --threshold 30 [path]
```

The command reads Istanbul `coverage-final.json`, computes cyclomatic
complexity for each authored function, and assigns coverage statements to the
innermost containing function. Its score is:

```text
CRAP = complexity² × (1 - coverage)³ + complexity
```

`--threshold` is the maximum allowed CRAP score and defaults to `30`. The
command exits nonzero when any function exceeds it. JSON includes each
function's canonical symbol, range, complexity, coverage fraction, and score.


## Describe a symbol

```sh
./slick describe <symbol> [path]
./slick describe --json <symbol> [path]
```

Slick resolves local functions, class methods, namespaces, and reachable
dependency exports. The contract contains structured type parameters,
parameters, and return types; execution mode; errors and effects with
provenance; completeness and unresolved leaves; source location; and exact
package identity where applicable. Human output renders the same document.

Short names are accepted only when unambiguous. Unknown and ambiguous names
return nonzero with deterministic `error.alternatives`.

## Build a project

```sh
./slick build [path]
./slick build --json [path]
```

Build runs the same TypeScript and Slick checks, then delegates JavaScript,
declaration, and source-map generation to TypeScript 5.9.3. TypeScript compiler
options remain authoritative. Slick does not rewrite functions, inject an async
runtime, or add an Effect dependency.

Emit is staged before installation. A TypeScript error, Slick error, interrupt,
emit failure, or output-install failure leaves pre-existing outputs unchanged.
Successful JSON output lists installed files relative to the project.

## Development loop

1. Inspect a symbol with `slick describe --json`.
2. Write ordinary TypeScript and npm imports.
3. Run `slick check --json` and apply its concrete repairs.
4. Inspect any exact unresolved package leaf.
5. Run `slick build`, then execute the emitted JavaScript normally.

## Failures and exit status

Structured failures use these `error.kind` values.

- `missing_configuration`
- `invalid_configuration`
- `missing_toolchain`
- `unsupported_toolchain`
- `project_reference`
- `analyzer_failure`
- `unknown_symbol`
- `ambiguous_symbol`
- `emit_failure`
- `coverage_failure`

Slick uses these exit codes.

| Code | Meaning |
| ---: | --- |
| `0` | TypeScript and Slick checking succeeded. |
| `1` | TypeScript or Slick reported an error, or analysis failed. |
| `2` | The command arguments were invalid. |
| `130` | An interrupt stopped the command. |

When Slick receives an interrupt, Slick stops all analyzer child processes.

## TypeScript resolution

Slick resolves TypeScript from the checked project. Install dependencies
before you run Slick.

```sh
npm install --save-dev typescript@5.9.3
```

Set `SLICK_TYPESCRIPT_PATH` only when normal project resolution is not
available.

```sh
SLICK_TYPESCRIPT_PATH=/absolute/path/to/typescript.js ./slick check
```

The specified compiler must be TypeScript 5.9.3.

## Operational analysis

The internal semantic model records facts for reachable authored and package code.

- Concrete `Error` subclasses
- Synchronous or asynchronous error delivery
- Authority effects
- Exact source ranges
- Unresolved calls

The model propagates facts through local calls. The fixed-point solver
converges on recursive call graphs. TypeScript symbols resolve authored call
targets. Typed `catch` guards remove only handled errors.

Slick follows reachable package exports into available implementation source,
including transitive package calls. Declaration-only, native, dynamic, and
otherwise unmodeled leaves remain unresolved with package identity and evidence.
Dependency summaries are cached by package artifact and analysis configuration.

## Test

Install the pinned TypeScript dependency before you run tests.

```sh
npm ci
```

Run the complete test suite.

```sh
go test -count=1 ./...
```

Run the operational contract scenarios.

```sh
go test -count=1 -v -run '^TestOperational' ./internal/app
```

Run static checks.

```sh
go vet ./...
node --check internal/app/analyzer.mjs
node --check internal/app/describe.mjs
node --check internal/app/build.mjs
node --check internal/app/crap.mjs
node --check internal/app/packages.mjs
node --check internal/app/strict.mjs
node --check internal/app/operational.mjs
```

The public CLI tests cover these cases.

- Valid and invalid projects
- Configuration discovery
- Deterministic JSON
- Infrastructure failures
- Interrupt cleanup
- TypeScript-equivalent build output and source maps
- Atomic output installation and rollback
- CRAP complexity, coverage, and threshold behavior

The operational tests combine these cases.

- Recursive and asynchronous calls
- Scoped symbols and caught errors
- Authority effects and provenance
- Unresolved calls

## Scope

Slick accepts normal TypeScript source. Slick does not add syntax extensions.
The current commands do not watch files, start a daemon, or run an editor
server.

See the [open issues](https://github.com/marcelsud/slick-ts/issues) for the
remaining implementation plan.
