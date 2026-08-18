# slick-ts

`slick` loads a TypeScript project through the TypeScript 5.9.3 Compiler API
and reports its authoritative parse, module-resolution, and type-checking
diagnostics.

## Build

Node.js 20 or newer and Go 1.24 or newer are required.

```sh
npm ci
go build -o slick ./cmd/slick
```

## Check a project

```sh
slick check [path]
slick check --json [path]
```

`path` may be a project directory, a nested source directory, a source file,
or a `tsconfig.json`. Slick searches that location and its parents for the
applicable configuration. The project must install the pinned TypeScript
5.9.3 version. `SLICK_TYPESCRIPT_PATH` may point to that compiler when it
cannot be resolved from the project.

Human output uses TypeScript diagnostic codes and messages. JSON output is a
deterministically ordered version 1 document with relative slash-separated
paths and 1-based lines and columns:

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

Structured failures use `error.kind`: `missing_configuration`,
`invalid_configuration`, `missing_toolchain`, `unsupported_toolchain`,
`project_reference`, or `analyzer_failure`. The exit status is zero only when
project loading and TypeScript checking succeed.
