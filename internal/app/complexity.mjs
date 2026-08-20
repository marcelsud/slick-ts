function collectComplexity(program, projectRoot, ts, graph) {
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
          ts.SyntaxKind.AmpersandAmpersandEqualsToken,
          ts.SyntaxKind.BarBarEqualsToken,
          ts.SyntaxKind.QuestionQuestionEqualsToken,
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
        const mapped = graphByLocation.get(locationKey);
        functions.push({
          node,
          start,
          end,
          symbol: mapped && !mapped.includes("callback:")
            ? mapped
            : `${stablePath(sourceFile.fileName)}::anonymous@${position.line + 1}:${position.character + 1}`,
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

  const results = [];
  for (const functions of functionsByFile.values()) {
    for (const functionRecord of functions) {
      results.push({
        symbol: functionRecord.symbol,
        path: functionRecord.path,
        range: functionRecord.range,
        complexity: functionRecord.complexity,
      });
    }
  }
  results.sort((left, right) => {
    const pathOrder = left.path < right.path ? -1 : left.path > right.path ? 1 : 0;
    const symbolOrder = left.symbol < right.symbol ? -1 : left.symbol > right.symbol ? 1 : 0;
    return pathOrder || left.range.start.offset - right.range.start.offset || symbolOrder;
  });
  return { functionsByFile, results };
}
