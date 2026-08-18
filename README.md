# Slick

Slick is a Go command-line interface for normal TypeScript projects. The
TypeScript Compiler API is the source of truth. TypeScript owns parsing,
module resolution, and type checking.

Slick is also the foundation for machine-readable operational contracts.
These contracts will describe errors, authority use, and asynchronous
ownership without custom TypeScript syntax or a new runtime.

## Status

`slick check` is available now. The command returns TypeScript diagnostics in
human or versioned JSON output.

The analyzer also creates internal operational summaries for authored source.
Each summary contains:

- Concrete errors and authority effects
- Synchronous or asynchronous execution mode
- Source provenance
- Unresolved calls

`slick check` does not expose operational summaries yet. A later
`slick describe` command will define the public contract.

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

Slick loads the configured project. Slick then runs TypeScript parsing,
module resolution, and type checking. Slick does not emit JavaScript.

### Human output

Human output keeps TypeScript diagnostic codes and messages.

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

## Failures and exit status

Structured failures use these `error.kind` values.

- `missing_configuration`
- `invalid_configuration`
- `missing_toolchain`
- `unsupported_toolchain`
- `project_reference`
- `analyzer_failure`

Slick uses these exit codes.

| Code | Meaning |
| ---: | --- |
| `0` | Project loading and TypeScript checking succeeded. |
| `1` | TypeScript reported an error, or Slick could not analyze the project. |
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

The internal semantic model records facts for reachable authored code.

- Concrete `Error` subclasses
- Synchronous or asynchronous error delivery
- Authority effects
- Exact source ranges
- Unresolved calls

The model propagates facts through local calls. The fixed-point solver
converges on recursive call graphs. TypeScript symbols resolve authored call
targets. Typed `catch` guards remove only handled errors.

Package implementation analysis is not available yet. Slick keeps a
declaration-only dependency call unresolved. Slick does not mark the call
safe.

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
node --check internal/app/operational.mjs
```

The public CLI tests cover these cases.

- Valid and invalid projects
- Configuration discovery
- Deterministic JSON
- Infrastructure failures
- Interrupt cleanup

The operational tests combine these cases.

- Recursive and asynchronous calls
- Scoped symbols and caught errors
- Authority effects and provenance
- Unresolved calls

## Scope

Slick accepts normal TypeScript source. Slick does not add syntax extensions.
The current command does not do these tasks.

- Emit JavaScript
- Watch files
- Start a daemon
- Run an editor server

See the [open issues](https://github.com/marcelsud/slick-ts/issues) for the
remaining implementation plan.
