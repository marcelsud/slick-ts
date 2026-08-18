import fs from "node:fs";
import crypto from "node:crypto";
import path from "node:path";
import { createRequire } from "node:module";

const supportedTypeScript = "5.9.3";
const configPath = path.resolve(process.env.SLICK_CONFIG_PATH);
const projectRoot = path.dirname(configPath);

function response(diagnostics = [], failure, graph = [], cache = { hits: 0, misses: 0 }, descriptions = [], outputs = []) {
  return JSON.stringify({ diagnostics, graph, cache, descriptions, outputs, ...(failure && { failure }) });
}

function failure(kind, message, diagnostics = []) {
  fs.writeFileSync(1, response(diagnostics, { kind, message }));
  process.exit(0);
}

let ts;
try {
  const require = createRequire(configPath);
  const requested = process.env.SLICK_TYPESCRIPT_PATH;
  const compilerPath = requested
    ? path.resolve(requested)
    : require.resolve("typescript", { paths: [projectRoot] });
  ts = require(compilerPath);
} catch {
  failure(
    "missing_toolchain",
    "TypeScript 5.9.3 was not found; install the project's pinned TypeScript dependency",
  );
}

if (ts.version !== supportedTypeScript) {
  failure(
    "unsupported_toolchain",
    `TypeScript ${ts.version} is unsupported; expected ${supportedTypeScript}`,
  );
}

const categories = ["warning", "error", "suggestion", "message"];

function stablePath(fileName) {
  return path.relative(projectRoot, path.resolve(fileName)).split(path.sep).join("/") || "tsconfig.json";
}

function convertDiagnostic(diagnostic) {
  const converted = {
    source: "typescript",
    code: diagnostic.code,
    category: categories[diagnostic.category] ?? "message",
    message: ts.flattenDiagnosticMessageText(diagnostic.messageText, "\n"),
  };
  if (diagnostic.file) {
    converted.path = stablePath(diagnostic.file.fileName);
    if (diagnostic.start !== undefined) {
      const start = diagnostic.file.getLineAndCharacterOfPosition(diagnostic.start);
      const endOffset = diagnostic.start + (diagnostic.length ?? 0);
      const end = diagnostic.file.getLineAndCharacterOfPosition(endOffset);
      converted.range = {
        start: { line: start.line + 1, column: start.character + 1, offset: diagnostic.start },
        end: { line: end.line + 1, column: end.character + 1, offset: endOffset },
      };
    }
  }
  return converted;
}

function compare(a, b) {
  const pathOrder = (a.path ?? "") < (b.path ?? "") ? -1 : (a.path ?? "") > (b.path ?? "") ? 1 : 0;
  return (
    pathOrder ||
    (a.range?.start.offset ?? -1) - (b.range?.start.offset ?? -1) ||
    a.code - b.code ||
    (a.message < b.message ? -1 : a.message > b.message ? 1 : 0) ||
    (a.category < b.category ? -1 : a.category > b.category ? 1 : 0)
  );
}

function normalize(diagnostics) {
  const unique = new Map();
  for (const diagnostic of diagnostics.map((value) => value.source === "slick" ? value : convertDiagnostic(value))) {
    const key = JSON.stringify(diagnostic);
    unique.set(key, diagnostic);
  }
  return [...unique.values()].sort(compare);
}

function loadConfig(fileName) {
  const unrecoverable = [];
  const host = {
    ...ts.sys,
    onUnRecoverableConfigFileDiagnostic: (diagnostic) => unrecoverable.push(diagnostic),
  };
  const parsed = ts.getParsedCommandLineOfConfigFile(fileName, {}, host);
  return {
    parsed,
    errors: parsed ? [...unrecoverable, ...parsed.errors] : unrecoverable,
  };
}

const rootConfig = loadConfig(configPath);
if (!rootConfig.parsed || rootConfig.errors.length > 0) {
  failure(
    "invalid_configuration",
    "TypeScript configuration is invalid",
    normalize(rootConfig.errors),
  );
}
const parsed = rootConfig.parsed;

function collectReferenceDiagnostics(commandLine, seen) {
  const diagnostics = [];
  for (const reference of commandLine.projectReferences ?? []) {
    const referencePath = path.resolve(ts.resolveProjectReferencePath(reference));
    if (seen.has(referencePath)) {
      continue;
    }
    seen.add(referencePath);
    const referenced = loadConfig(referencePath);
    diagnostics.push(...referenced.errors);
    if (referenced.parsed && referenced.errors.length === 0) {
      diagnostics.push(...collectReferenceDiagnostics(referenced.parsed, seen));
    }
  }
  return diagnostics;
}

const referenceDiagnostics = collectReferenceDiagnostics(parsed, new Set([configPath]));
if (referenceDiagnostics.length > 0) {
  failure(
    "project_reference",
    "a referenced TypeScript project could not be loaded",
    normalize(referenceDiagnostics),
  );
}

const program = ts.createProgram({
  rootNames: parsed.fileNames,
  options: parsed.options,
  projectReferences: parsed.projectReferences,
});
const diagnostics = ts.getPreEmitDiagnostics(program);
const projectReferenceFailure = diagnostics.some(
  ({ code }) => (code >= 6305 && code <= 6312) || code === 6377 || code === 6378,
);
const resolved = projectReferenceFailure
  ? { program, packages: [] }
  : resolvePackageImplementations(program, parsed, projectRoot, ts);
const operational = projectReferenceFailure
  ? { graph: [], cache: { hits: 0, misses: 0 } }
  : analyzeOperational(resolved.program, projectRoot, ts, resolved.packages);
const descriptions = projectReferenceFailure
  ? []
  : analyzeDescriptions(resolved.program, operational.graph, projectRoot, ts, resolved.packages);
const slickDiagnostics = projectReferenceFailure
  ? []
  : analyzeStrict(program, projectRoot, ts, diagnostics);
const build = projectReferenceFailure
  ? { diagnostics: [], outputs: [] }
  : emitBuild(program, ts, diagnostics, slickDiagnostics);
process.stdout.write(
  response(
    normalize([...diagnostics, ...slickDiagnostics, ...build.diagnostics]),
    projectReferenceFailure
      ? { kind: "project_reference", message: "a referenced TypeScript project could not be checked" }
      : build.failure,
    operational.graph,
    operational.cache,
    descriptions,
    build.outputs,
  ),
);
