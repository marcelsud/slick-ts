# Slick

Slick is a Go command-line interface for normal TypeScript projects. The
TypeScript Compiler API is the source of truth. TypeScript owns parsing,
module resolution, and type checking.

Slick exposes machine-readable operational contracts for errors, authority
use, asynchronous ownership, and unresolved dependencies without custom
TypeScript syntax or a new runtime.

## Status

Slick provides project checks, contract inspection, TypeScript builds, and
deterministic quality-analysis commands. Every command has human and versioned
JSON output.

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

## Check complexity

```sh
./slick complexity [--json] [--threshold 10] [path]
```

The command measures cyclomatic complexity for authored functions, methods,
accessors, constructors, and bound function expressions. It uses the same
function inventory and decision rules as `slick crap`. Nested callables keep
their own scores. Coverage is not required.

Counted decisions are loops, `case` clauses, `catch` clauses, conditionals,
logical operators, and logical assignments.

`--threshold` is the maximum allowed complexity and defaults to `10`. A score
equal to the threshold passes. A score above it fails. The command exits
nonzero only when a function exceeds the threshold or analysis fails. JSON
includes each function's canonical symbol, exact range, and complexity.

## Report maintainability inputs

```sh
./slick maintainability [--json] [--threshold 20] [path]
```

Slick reports cyclomatic complexity, logical LOC, Halstead operator and operand
counts, volume, and the normalized maintainability index. A zero threshold only
reports values. A positive threshold fails functions below it. Formatting and
comments do not change the token counts or canonical LOC.

```text
MI = max(0, (171 - 5.2 ln(Halstead volume) - 0.23 cyclomatic - 16.2 ln(LOC)) * 100 / 171)
```




## Check C.R.A.P.

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

## Check coverage quality

```sh
./slick coverage --coverage coverage/coverage-final.json [--json] [--base ref] \
  [--branch-threshold 80] [--changed-line-threshold 90] \
  [--uncovered-complexity-threshold 10] [path]
```

The command validates Istanbul branch data, reports project and file branch
coverage, and assigns uncovered branch decisions to the innermost function.
When `--base` is set, Slick reads changed lines from Git and scores executable
changed lines only. Threshold equality passes. Missing coverage and invalid Git
references return structured failures.

## Rank changed code

```sh
./slick risk --base ref [--history 90d] [--coverage coverage-final.json] \
  [--config slick.risk.json] [--json] [path]
```

Risk ranking combines changed lines, Git churn, author count, complexity,
coverage, and module fan-in. `slick.risk.json` contains explicit weights and an
optional threshold. Missing coverage and shallow history stay visible instead
of becoming zero.



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

## Check declared resource bounds

```sh
./slick bounds [--json] [--contracts slick.contracts.json] [path]
```

The contract file maps canonical symbols or aliases to `timeoutMs`,
`maxAttempts`, `maxItems`, and `maxConcurrency`. Slick propagates known values
through calls, branches, and finite loops. Data-dependent loops, recursion, and
unresolved calls remain unknown. Unknown is not treated as a pass. `describe`
uses the same bound result when the default contract file exists.


## Diff public contracts

```sh
./slick api snapshot --output slick-api.json [--entry export]... [path]
./slick api diff --baseline slick-api.json [--json] [path]
```

Snapshots contain sorted exported signatures and operational contracts. Diff
uses structured types and directional compatibility checks. Removed exports,
narrower parameters, wider returns, removed overloads, new errors or effects,
reduced visibility, and complete-to-partial changes are breaking.


## Find unreachable code

```sh
./slick dead-code [--json] [--entry file]... [path]
```

Slick follows TypeScript exports, aliases, calls, classes, and bound callbacks
from each entry module. A project with no explicit entry uses an authored
`index.ts` variant. Unknown dynamic imports remain unknown and suppress dead
findings that Slick cannot prove.

## Enforce dependency architecture

```sh
./slick architecture [--json] [--config slick.architecture.json] [path]
```

The rule file assigns source globs to named layers and lists which layers each
layer may import. Slick reports TypeScript-resolved edges, runtime and type-only
cycles, fan-in, fan-out, layer violations, and unknown dynamic imports.
`maxFanIn`, `maxFanOut`, and `allowTypeOnlyCycles` control project-wide rules.

## Find duplicated implementation blocks

```sh
./slick duplication [--json] [--min-nodes 20] [--min-occurrences 2] [path]
```

Slick normalizes authored function blocks, including local identifier names,
then groups matching AST fingerprints. Operators, control flow, property names,
called names, and literal values remain significant. Tests, dependencies,
declarations, snapshots, and emitted output are excluded.

## Measure test sensitivity

```sh
./slick mutate [--json] [--timeout 30s] [--max-mutants 200] \
  [--coverage coverage-final.json] [path] -- <test command> [args...]
```

Slick creates deterministic first-order TypeScript mutants in an isolated
project. It type-checks each mutant, runs the supplied command without a shell,
enforces a timeout, and reports killed, survived, invalid, timed-out, and
uncovered mutants. The original project is never edited.





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

## Check emitted artifacts

```sh
./slick artifacts [--json] [--max-total-bytes N] [--max-file-bytes N] \
  [--deny-runtime-import package] [path]
```

The command stages the same TypeScript output as `slick build`, then checks
total bytes, per-file bytes, and bare runtime package imports. Rejected
artifacts are not installed. Existing outputs remain unchanged. Repeat
`--deny-runtime-import` to block more than one package.


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
- `api_snapshot_failure`
- `api_baseline_failure`
- `api_diff_failure`
- `git_failure`
- `risk_configuration`
- `mutation_failure`
- `test_command_failure`
- `bounds_configuration`
- `bounds_overflow`
- `entry_configuration`
- `architecture_configuration`

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
node --check internal/app/artifacts.mjs
node --check internal/app/complexity.mjs
node --check internal/app/coverage.mjs
node --check internal/app/crap.mjs
node --check internal/app/maintainability.mjs
node --check internal/app/risk.mjs
node --check internal/app/mutation.mjs
node --check internal/app/bounds.mjs
node --check internal/app/duplication.mjs
node --check internal/app/architecture.mjs
node --check internal/app/deadcode.mjs
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
- Standalone complexity thresholds and decision coverage
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
