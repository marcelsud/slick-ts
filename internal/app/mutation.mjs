function analyzeMutants(program, projectRoot, ts, collected) {
  if (process.env.SLICK_MUTATION !== "1") return [];
  function stablePath(fileName) { return path.relative(projectRoot, path.resolve(fileName)).split(path.sep).join("/"); }
  function authored(sourceFile) {
    const relative = stablePath(sourceFile.fileName);
    return !sourceFile.isDeclarationFile && !relative.startsWith("../") && !relative.split("/").includes("node_modules") &&
      !relative.split("/").includes("dist") && !relative.split("/").includes("__tests__") && !/\.(test|spec)\.[cm]?tsx?$/.test(relative);
  }
  function sourceRange(node) {
    const sourceFile = node.getSourceFile();
    const startOffset = node.getStart(sourceFile), endOffset = node.getEnd();
    const start = sourceFile.getLineAndCharacterOfPosition(startOffset), end = sourceFile.getLineAndCharacterOfPosition(endOffset);
    return { start: { line: start.line + 1, column: start.character + 1, offset: startOffset }, end: { line: end.line + 1, column: end.character + 1, offset: endOffset } };
  }
  function containingSymbol(fileName, node) {
    const functions = collected.functionsByFile.get(path.resolve(fileName)) ?? [];
    const start = node.getStart(node.getSourceFile()), end = node.getEnd();
    let owner;
    for (const candidate of functions) if (candidate.start <= start && candidate.end >= end && (!owner || candidate.start >= owner.start && candidate.end <= owner.end)) owner = candidate;
    return owner?.symbol ?? "";
  }
  const replacements = new Map([
    [ts.SyntaxKind.EqualsEqualsEqualsToken, "!=="], [ts.SyntaxKind.ExclamationEqualsEqualsToken, "==="],
    [ts.SyntaxKind.EqualsEqualsToken, "!="], [ts.SyntaxKind.ExclamationEqualsToken, "=="],
    [ts.SyntaxKind.GreaterThanToken, "<="], [ts.SyntaxKind.GreaterThanEqualsToken, "<"],
    [ts.SyntaxKind.LessThanToken, ">="], [ts.SyntaxKind.LessThanEqualsToken, ">"],
    [ts.SyntaxKind.PlusToken, "-"], [ts.SyntaxKind.MinusToken, "+"],
    [ts.SyntaxKind.AsteriskToken, "/"], [ts.SyntaxKind.SlashToken, "*"],
  ]);
  const mutants = [];
  function add(sourceFile, node, replacement, operator) {
    const range = sourceRange(node);
    const value = `${stablePath(sourceFile.fileName)}:${range.start.offset}:${range.end.offset}:${replacement}`;
    mutants.push({
      id: crypto.createHash("sha256").update(value).digest("hex").slice(0, 16),
      path: stablePath(sourceFile.fileName), range, replacement, operator,
      original: node.getText(sourceFile), symbol: containingSymbol(sourceFile.fileName, node),
    });
  }
  function mutableLiteral(node) {
    const parent = node.parent;
    return !(ts.isImportDeclaration(parent) || ts.isExportDeclaration(parent) ||
      ts.isExternalModuleReference(parent) ||
      (parent?.name === node && (ts.isPropertyDeclaration(parent) || ts.isPropertyAssignment(parent) || ts.isMethodDeclaration(parent))));
  }

  for (const sourceFile of program.getSourceFiles()) {
    if (!authored(sourceFile)) continue;
    function visit(node) {
      if (node.kind === ts.SyntaxKind.TrueKeyword) add(sourceFile, node, "false", "boolean");
      else if (node.kind === ts.SyntaxKind.FalseKeyword) add(sourceFile, node, "true", "boolean");
      else if (ts.isNumericLiteral(node) && mutableLiteral(node)) add(sourceFile, node, node.text === "0" ? "1" : "0", "number");
      else if (ts.isStringLiteral(node) && mutableLiteral(node)) add(sourceFile, node, node.text === "" ? '"mutant"' : '""', "string");
      else if (ts.isBinaryExpression(node) && replacements.has(node.operatorToken.kind)) add(sourceFile, node.operatorToken, replacements.get(node.operatorToken.kind), "operator");
      else if (ts.isIfStatement(node) || ts.isConditionalExpression(node) || ts.isWhileStatement(node) || ts.isDoStatement(node) || ts.isForStatement(node) && node.condition) {
        const expression = ts.isConditionalExpression(node) ? node.condition : ts.isForStatement(node) ? node.condition : node.expression;
        if (expression) { add(sourceFile, expression, "true", "condition"); add(sourceFile, expression, "false", "condition"); }
      }
      ts.forEachChild(node, visit);
    }
    visit(sourceFile);
  }
  mutants.sort((left, right) => left.path < right.path ? -1 : left.path > right.path ? 1 : left.range.start.offset - right.range.start.offset || (left.replacement < right.replacement ? -1 : 1));
  return mutants;
}
