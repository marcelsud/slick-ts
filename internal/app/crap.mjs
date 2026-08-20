function analyzeCrap(program, projectRoot, ts, graph, coveragePath, collected) {
  if (!coveragePath) return { results: [] };
  let coverage;
  try {
    coverage = JSON.parse(fs.readFileSync(coveragePath, "utf8"));
  } catch (error) {
    return {
      results: [],
      failure: {
        kind: "coverage_failure",
        message: error instanceof Error ? error.message : String(error),
      },
    };
  }
  if (!coverage || typeof coverage !== "object" || Array.isArray(coverage)) {
    return { results: [], failure: { kind: "coverage_failure", message: "coverage document must be an object" } };
  }
  function record(value) {
    return Boolean(value) && typeof value === "object" && !Array.isArray(value);
  }

  function point(value) {
    return record(value) && Number.isInteger(value.line) && value.line >= 1 &&
      Number.isInteger(value.column) && value.column >= 0;
  }

  function location(value) {
    return record(value) && point(value.start) && point(value.end);
  }

  function counter(value) {
    return typeof value === "number" && Number.isFinite(value) && value >= 0;
  }

  function validateFileCoverage(name, value) {
    if (!record(value)) return `${name} must be an object`;
    if (value.path !== undefined && typeof value.path !== "string") return `${name}.path must be a string`;
    if (!record(value.statementMap) || !record(value.s)) {
      return `${name}.statementMap and ${name}.s must be objects`;
    }
    for (const [identifier, entry] of Object.entries(value.statementMap)) {
      if (!location(entry)) return `${name}.statementMap.${identifier} has an invalid location`;
      if (!counter(value.s[identifier])) return `${name}.s.${identifier} must be a non-negative number`;
    }
    if (!record(value.fnMap) || !record(value.f)) {
      return `${name}.fnMap and ${name}.f must be objects`;
    }
    for (const [identifier, entry] of Object.entries(value.fnMap)) {
      if (!record(entry) || !location(entry.loc ?? entry.decl)) {
        return `${name}.fnMap.${identifier} has an invalid location`;
      }
      if (!counter(value.f[identifier])) return `${name}.f.${identifier} must be a non-negative number`;
    }
    return undefined;
  }

  for (const [name, value] of Object.entries(coverage)) {
    const invalid = validateFileCoverage(name, value);
    if (invalid) {
      return { results: [], failure: { kind: "coverage_failure", message: invalid } };
    }
  }


  const functionsByFile = (collected ?? collectComplexity(program, projectRoot, ts, graph)).functionsByFile;

  function coverageFileFor(fileName) {
    const absolute = path.resolve(fileName);
    for (const [key, value] of Object.entries(coverage)) {
      const candidates = [key, value?.path].filter(Boolean).flatMap((candidate) => [
        path.resolve(candidate),
        path.resolve(projectRoot, candidate),
      ]);
      if (candidates.includes(absolute)) return value;
    }
    return undefined;
  }

  function offsetFor(sourceFile, point) {
    if (!point || !Number.isInteger(point.line) || !Number.isInteger(point.column) || point.line < 1 || point.column < 0) {
      return undefined;
    }
    try {
      return sourceFile.getPositionOfLineAndCharacter(point.line - 1, point.column);
    } catch {
      return undefined;
    }
  }

  function innermost(functions, start, end) {
    let result;
    for (const candidate of functions) {
      if (candidate.start <= start && candidate.end >= end &&
          (!result || candidate.start >= result.start && candidate.end <= result.end)) {
        result = candidate;
      }
    }
    return result;
  }

  for (const [fileName, functions] of functionsByFile) {
    const sourceFile = program.getSourceFile(fileName) ??
      program.getSourceFiles().find((value) => path.resolve(value.fileName) === fileName);
    const fileCoverage = coverageFileFor(fileName);
    if (!sourceFile || !fileCoverage) continue;
    for (const [identifier, location] of Object.entries(fileCoverage.statementMap ?? {})) {
      const start = offsetFor(sourceFile, location.start);
      const end = offsetFor(sourceFile, location.end);
      if (start === undefined || end === undefined) continue;
      const owner = innermost(functions, start, end);
      if (!owner) continue;
      owner.statements++;
      if (Number(fileCoverage.s?.[identifier] ?? 0) > 0) owner.covered++;
    }
    for (const functionRecord of functions) {
      if (functionRecord.statements > 0) continue;
      const match = Object.entries(fileCoverage.fnMap ?? {}).find(([, entry]) => {
        const start = offsetFor(sourceFile, entry.loc?.start ?? entry.decl?.start);
        const end = offsetFor(sourceFile, entry.loc?.end ?? entry.decl?.end);
        return start !== undefined && end !== undefined &&
          functionRecord.start <= start && functionRecord.end >= end;
      });
      if (match) {
        functionRecord.statements = 1;
        functionRecord.covered = Number(fileCoverage.f?.[match[0]] ?? 0) > 0 ? 1 : 0;
      }
    }
  }

  const results = [];
  for (const functions of functionsByFile.values()) {
    for (const functionRecord of functions) {
      const functionCoverage = functionRecord.statements === 0
        ? 0
        : functionRecord.covered / functionRecord.statements;
      const score = functionRecord.complexity ** 2 * (1 - functionCoverage) ** 3 + functionRecord.complexity;
      results.push({
        symbol: functionRecord.symbol,
        path: functionRecord.path,
        range: functionRecord.range,
        complexity: functionRecord.complexity,
        coverage: functionCoverage,
        score,
      });
    }
  }
  results.sort((left, right) => {
    const pathOrder = left.path < right.path ? -1 : left.path > right.path ? 1 : 0;
    const symbolOrder = left.symbol < right.symbol ? -1 : left.symbol > right.symbol ? 1 : 0;
    return pathOrder || left.range.start.offset - right.range.start.offset || symbolOrder;
  });
  return { results };
}
