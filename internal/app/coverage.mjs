function analyzeCoverageQuality(program, projectRoot, ts, coveragePath, collected, crap) {
  if (!coveragePath || process.env.SLICK_COVERAGE_QUALITY !== "1") return { report: undefined };
  if (crap.failure) return { report: undefined, failure: crap.failure };

  let coverage;
  try {
    coverage = JSON.parse(fs.readFileSync(coveragePath, "utf8"));
  } catch (error) {
    return { report: undefined, failure: { kind: "coverage_failure", message: error instanceof Error ? error.message : String(error) } };
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

  function validateBranches(name, value) {
    if (!record(value.branchMap) || !record(value.b)) return `${name}.branchMap and ${name}.b must be objects`;
    for (const [identifier, entry] of Object.entries(value.branchMap)) {
      const counts = value.b[identifier];
      if (!record(entry) || !location(entry.loc) || !Array.isArray(entry.locations) || !Array.isArray(counts) || entry.locations.length !== counts.length) {
        return `${name}.branchMap.${identifier} is invalid`;
      }
      if (!entry.locations.every(location) || !counts.every((count) => typeof count === "number" && Number.isFinite(count) && count >= 0)) {
        return `${name}.branchMap.${identifier} has invalid locations or counters`;
      }
    }
    return undefined;
  }

  for (const [name, value] of Object.entries(coverage)) {
    const invalid = validateBranches(name, value);
    if (invalid) return { report: undefined, failure: { kind: "coverage_failure", message: invalid } };
  }

  function stablePath(fileName) {
    return path.relative(projectRoot, path.resolve(fileName)).split(path.sep).join("/");
  }

  function coverageFor(fileName) {
    const absolute = path.resolve(fileName);
    for (const [key, value] of Object.entries(coverage)) {
      const candidates = [key, value.path].filter(Boolean).flatMap((candidate) => [path.resolve(candidate), path.resolve(projectRoot, candidate)]);
      if (candidates.includes(absolute)) return value;
    }
    return undefined;
  }

  function offsetFor(sourceFile, value) {
    try {
      return sourceFile.getPositionOfLineAndCharacter(value.line - 1, value.column);
    } catch {
      return undefined;
    }
  }

  function owner(functions, start, end) {
    let result;
    for (const candidate of functions) {
      if (candidate.start <= start && candidate.end >= end && (!result || candidate.start >= result.start && candidate.end <= result.end)) {
        result = candidate;
      }
    }
    return result;
  }

  function executableLines(sourceFile) {
    const lines = new Set();
    function typeOnly(node) {
      if (ts.isImportDeclaration(node)) {
        if (node.importClause?.isTypeOnly) return true;
        const bindings = node.importClause?.namedBindings;
        return bindings && ts.isNamedImports(bindings) && bindings.elements.length > 0 && bindings.elements.every((element) => element.isTypeOnly);
      }
      if (ts.isExportDeclaration(node)) {
        if (node.isTypeOnly) return true;
        return node.exportClause && ts.isNamedExports(node.exportClause) && node.exportClause.elements.length > 0 && node.exportClause.elements.every((element) => element.isTypeOnly);
      }
      return ts.isInterfaceDeclaration(node) || ts.isTypeAliasDeclaration(node);
    }
    function visit(node) {
      if (ts.isStatement(node) && !ts.isBlock(node) && !ts.isEmptyStatement(node) && !typeOnly(node)) {
        lines.add(sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile)).line + 1);
      }
      ts.forEachChild(node, visit);
    }
    visit(sourceFile);
    return lines;
  }

  const files = [];
  const functions = [];
  let branchCovered = 0;
  let branchTotal = 0;
  for (const sourceFile of program.getSourceFiles()) {
    const absolute = path.resolve(sourceFile.fileName);
    const callableRecords = collected.functionsByFile.get(absolute);
    if (!callableRecords) continue;
    const fileCoverage = coverageFor(absolute);
    const lineState = new Map([...executableLines(sourceFile)].map((line) => [line, false]));
    let fileBranchCovered = 0;
    let fileBranchTotal = 0;

    if (fileCoverage) {
      for (const [identifier, statement] of Object.entries(fileCoverage.statementMap)) {
        const line = statement.start.line;
        lineState.set(line, Boolean(lineState.get(line)) || Number(fileCoverage.s[identifier]) > 0);
      }
      for (const [identifier, branch] of Object.entries(fileCoverage.branchMap)) {
        const counts = fileCoverage.b[identifier];
        fileBranchTotal += counts.length;
        fileBranchCovered += counts.filter((count) => count > 0).length;
        const start = offsetFor(sourceFile, branch.loc.start);
        const end = offsetFor(sourceFile, branch.loc.end);
        const callable = start === undefined || end === undefined ? undefined : owner(callableRecords, start, end);
        if (callable && counts.some((count) => count === 0)) callable.uncoveredDecisions = (callable.uncoveredDecisions ?? 0) + 1;
      }
    }

    branchCovered += fileBranchCovered;
    branchTotal += fileBranchTotal;
    files.push({
      path: stablePath(absolute),
      branchCovered: fileBranchCovered,
      branchTotal: fileBranchTotal,
      lines: [...lineState].sort((left, right) => left[0] - right[0]).map(([line, covered]) => ({ line, covered })),
    });
    for (const callable of callableRecords) {
      functions.push({
        symbol: callable.symbol,
        path: callable.path,
        range: callable.range,
        complexity: callable.complexity,
        uncoveredDecisions: callable.uncoveredDecisions ?? 0,
      });
    }
  }

  files.sort((left, right) => left.path < right.path ? -1 : left.path > right.path ? 1 : 0);
  functions.sort((left, right) => {
    const pathOrder = left.path < right.path ? -1 : left.path > right.path ? 1 : 0;
    return pathOrder || left.range.start.offset - right.range.start.offset;
  });
  return { report: { branchCovered, branchTotal, files, functions } };
}
