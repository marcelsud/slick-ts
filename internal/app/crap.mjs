function analyzeCrap(program, projectRoot, ts, graph, coveragePath) {
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

  function stablePath(fileName) {
    return path.relative(projectRoot, path.resolve(fileName)).split(path.sep).join("/");
  }

  function isCallable(node) {
    return ts.isFunctionDeclaration(node) || ts.isMethodDeclaration(node) ||
      ts.isGetAccessorDeclaration(node) || ts.isSetAccessorDeclaration(node) ||
      ts.isConstructorDeclaration(node) || ts.isFunctionExpression(node) || ts.isArrowFunction(node);
  }

  function nameNode(node) {
    if (node.name) return node.name;
    if (ts.isConstructorDeclaration(node)) return node.parent.name ?? node;
    const parent = node.parent;
    if ((ts.isVariableDeclaration(parent) || ts.isPropertyDeclaration(parent) || ts.isPropertyAssignment(parent)) && parent.name) {
      return parent.name;
    }
    return node;
  }

  function sourceRange(node) {
    const sourceFile = node.getSourceFile();
    const startOffset = node.getStart(sourceFile);
    const endOffset = node.getEnd();
    const start = sourceFile.getLineAndCharacterOfPosition(startOffset);
    const end = sourceFile.getLineAndCharacterOfPosition(endOffset);
    return {
      start: { line: start.line + 1, column: start.character + 1, offset: startOffset },
      end: { line: end.line + 1, column: end.character + 1, offset: endOffset },
    };
  }

  function complexityOf(callable) {
    let complexity = 1;
    function visit(node) {
      if (node !== callable && isCallable(node)) return;
      if (
        ts.isIfStatement(node) || ts.isForStatement(node) || ts.isForInStatement(node) ||
        ts.isForOfStatement(node) || ts.isWhileStatement(node) || ts.isDoStatement(node) ||
        ts.isCatchClause(node) || ts.isConditionalExpression(node) || ts.isCaseClause(node)
      ) {
        complexity++;
      } else if (
        ts.isBinaryExpression(node) &&
        [
          ts.SyntaxKind.AmpersandAmpersandToken,
          ts.SyntaxKind.BarBarToken,
          ts.SyntaxKind.QuestionQuestionToken,
        ].includes(node.operatorToken.kind)
      ) {
        complexity++;
      }
      ts.forEachChild(node, visit);
    }
    if (callable.body) visit(callable.body);
    return complexity;
  }

  function authoredFile(sourceFile) {
    const relative = path.relative(projectRoot, path.resolve(sourceFile.fileName));
    return !sourceFile.isDeclarationFile && relative !== "" && relative !== ".." &&
      !relative.startsWith(`..${path.sep}`) && !relative.split(path.sep).includes("node_modules");
  }

  const graphByLocation = new Map(graph.map((entry) => [
    `${entry.location.path}\0${entry.location.range.start.offset}`,
    entry.symbol,
  ]));
  const functionsByFile = new Map();
  for (const sourceFile of program.getSourceFiles()) {
    if (!authoredFile(sourceFile)) continue;
    const functions = [];
    function discover(node) {
      if (isCallable(node) && node.body) {
        const named = nameNode(node);
        const range = sourceRange(named);
        const locationKey = `${stablePath(sourceFile.fileName)}\0${range.start.offset}`;
        const start = node.body.getStart(sourceFile);
        const end = node.body.getEnd();
        const position = sourceFile.getLineAndCharacterOfPosition(range.start.offset);
        functions.push({
          node,
          start,
          end,
          symbol: graphByLocation.get(locationKey) ??
            `${stablePath(sourceFile.fileName)}::anonymous@${position.line + 1}:${position.character + 1}`,
          path: stablePath(sourceFile.fileName),
          range,
          complexity: complexityOf(node),
          covered: 0,
          statements: 0,
        });
      }
      ts.forEachChild(node, discover);
    }
    discover(sourceFile);
    functions.sort((left, right) => left.start - right.start || right.end - left.end);
    functionsByFile.set(path.resolve(sourceFile.fileName), functions);
  }

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
  results.sort((left, right) => left.path.localeCompare(right.path) ||
    left.range.start.offset - right.range.start.offset || left.symbol.localeCompare(right.symbol));
  return { results };
}
