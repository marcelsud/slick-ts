function analyzeDuplication(program, projectRoot, ts, minNodes, minOccurrences) {
  if (process.env.SLICK_DUPLICATION !== "1") return { report: undefined };
  const checker = program.getTypeChecker();
  function stablePath(fileName) { return path.relative(projectRoot, path.resolve(fileName)).split(path.sep).join("/"); }
  function authored(sourceFile) {
    const relative = path.relative(projectRoot, path.resolve(sourceFile.fileName));
    const parts = relative.split(path.sep);
    return !sourceFile.isDeclarationFile && relative !== "" && relative !== ".." && !relative.startsWith(`..${path.sep}`) &&
      !parts.includes("node_modules") && !parts.includes("dist") && !parts.includes("__tests__") &&
      !/\.(test|spec)\.[cm]?tsx?$/.test(relative) && !relative.endsWith(".snap");
  }
  function resolveAlias(symbol) { return symbol && symbol.flags & ts.SymbolFlags.Alias ? checker.getAliasedSymbol(symbol) : symbol; }
  function range(node) {
    const sourceFile = node.getSourceFile();
    const startOffset = node.getStart(sourceFile), endOffset = node.getEnd();
    const start = sourceFile.getLineAndCharacterOfPosition(startOffset), end = sourceFile.getLineAndCharacterOfPosition(endOffset);
    return { start: { line: start.line + 1, column: start.character + 1, offset: startOffset }, end: { line: end.line + 1, column: end.character + 1, offset: endOffset } };
  }
  function isName(node) {
    const parent = node.parent;
    return (parent.name === node && (ts.isPropertyDeclaration(parent) || ts.isPropertyAssignment(parent) || ts.isMethodDeclaration(parent) || ts.isGetAccessorDeclaration(parent) || ts.isSetAccessorDeclaration(parent))) ||
      (ts.isPropertyAccessExpression(parent) && parent.name === node);
  }
  function countNodes(node) { let count = 1; ts.forEachChild(node, (child) => { count += countNodes(child); }); return count; }
  function enclosingCallable(node) {
    let current = node.parent;
    while (current) { if (ts.isFunctionLike(current)) return current; current = current.parent; }
    return undefined;
  }
  function normalized(node) {
    const localNames = new Map();
    let nextLocal = 0;
    const callable = enclosingCallable(node);
    for (const parameter of callable?.parameters ?? []) {
      if (ts.isIdentifier(parameter.name)) {
        const symbol = resolveAlias(checker.getSymbolAtLocation(parameter.name));
        if (symbol) localNames.set(symbol, `$${nextLocal++}`);
      }
    }
    function encode(current) {
      if (ts.isIdentifier(current)) {
        if (isName(current)) return [current.kind, current.text];
        const symbol = resolveAlias(checker.getSymbolAtLocation(current));
        if (symbol && localNames.has(symbol)) return [current.kind, localNames.get(symbol)];
        if (symbol && current.parent?.name === current && (ts.isVariableDeclaration(current.parent) || ts.isParameter(current.parent) || ts.isBindingElement(current.parent))) {
          const name = `$${nextLocal++}`;
          localNames.set(symbol, name);
          return [current.kind, name];
        }
        return [current.kind, current.text];
      }
      if (ts.isStringLiteralLike(current) || ts.isNumericLiteral(current) || current.kind === ts.SyntaxKind.TrueKeyword || current.kind === ts.SyntaxKind.FalseKeyword || current.kind === ts.SyntaxKind.NullKeyword) {
        return [current.kind, current.getText(current.getSourceFile())];
      }
      const children = [];
      ts.forEachChild(current, (child) => { children.push(encode(child)); });
      return [current.kind, children];
    }
    return JSON.stringify(encode(node));
  }

  const groups = new Map();
  for (const sourceFile of program.getSourceFiles()) {
    if (!authored(sourceFile)) continue;
    function visit(node) {
      const candidate = ts.isBlock(node) && node.statements.length > 0 ||
        ts.isBinaryExpression(node) || ts.isConditionalExpression(node) || ts.isCallExpression(node) ||
        ts.isObjectLiteralExpression(node) || ts.isArrayLiteralExpression(node);
      if (candidate) {
        const nodes = countNodes(node);
        if (nodes >= minNodes) {
          const form = normalized(node);
          const fingerprint = crypto.createHash("sha256").update(form).digest("hex");
          const group = groups.get(fingerprint) ?? { fingerprint, nodes, occurrences: [] };
          group.occurrences.push({ path: stablePath(sourceFile.fileName), range: range(node) });
          groups.set(fingerprint, group);
        }
      }
      ts.forEachChild(node, visit);
    }
    visit(sourceFile);
  }
  const candidates = [...groups.values()].filter((group) => group.occurrences.length >= minOccurrences);
  for (const clone of candidates) clone.occurrences.sort((left, right) => left.path < right.path ? -1 : left.path > right.path ? 1 : left.range.start.offset - right.range.start.offset);
  candidates.sort((left, right) => right.nodes - left.nodes || (left.fingerprint < right.fingerprint ? -1 : left.fingerprint > right.fingerprint ? 1 : 0));
  const clones = [];
  for (const candidate of candidates) {
    const contained = candidate.occurrences.every((occurrence) => clones.some((accepted) =>
      accepted.occurrences.some((parent) => parent.path === occurrence.path &&
        parent.range.start.offset <= occurrence.range.start.offset && parent.range.end.offset >= occurrence.range.end.offset)));
    if (!contained) clones.push(candidate);
  }
  return { report: { minNodes, minOccurrences, clones } };
}
