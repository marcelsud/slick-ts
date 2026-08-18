import path from "node:path";
import { createRequire } from "node:module";

const supportedTypeScript = "5.9.3";
const configPath = path.resolve(process.env.SLICK_CONFIG_PATH);
const projectRoot = path.dirname(configPath);

function response(diagnostics = [], failure) {
  process.stdout.write(JSON.stringify({ diagnostics, ...(failure && { failure }) }));
}

function failure(kind, message, diagnostics = []) {
  response(diagnostics, { kind, message });
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
  for (const diagnostic of diagnostics.map(convertDiagnostic)) {
    const key = JSON.stringify(diagnostic);
    unique.set(key, diagnostic);
  }
  return [...unique.values()].sort(compare);
}

const loaded = ts.readConfigFile(configPath, ts.sys.readFile);
if (loaded.error) {
  failure("invalid_configuration", "TypeScript configuration is invalid", normalize([loaded.error]));
}

const parsed = ts.parseJsonConfigFileContent(
  loaded.config,
  ts.sys,
  projectRoot,
  undefined,
  configPath,
);
if (parsed.errors.length > 0) {
  failure("invalid_configuration", "TypeScript configuration is invalid", normalize(parsed.errors));
}

const referenceDiagnostics = [];
for (const reference of parsed.projectReferences ?? []) {
  const referencePath = ts.resolveProjectReferencePath(reference);
  const referenced = ts.readConfigFile(referencePath, ts.sys.readFile);
  if (referenced.error) {
    referenceDiagnostics.push(referenced.error);
    continue;
  }
  const referencedConfig = ts.parseJsonConfigFileContent(
    referenced.config,
    ts.sys,
    path.dirname(referencePath),
    undefined,
    referencePath,
  );
  referenceDiagnostics.push(...referencedConfig.errors);
}
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
response(
  normalize(diagnostics),
  projectReferenceFailure
    ? { kind: "project_reference", message: "a referenced TypeScript project could not be checked" }
    : undefined,
);
