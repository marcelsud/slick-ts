function analyzeBounds(program, projectRoot, ts, graph, descriptions, contractsPath) {
  if (process.env.SLICK_BOUNDS !== "1") return { report: undefined };
  let contractFile;
  try { contractFile = JSON.parse(fs.readFileSync(contractsPath, "utf8")); }
  catch (error) { return { report: undefined, failure: { kind: "bounds_configuration", message: error instanceof Error ? error.message : String(error) } }; }
  if (!contractFile || !contractFile.symbols || typeof contractFile.symbols !== "object" || Array.isArray(contractFile.symbols)) {
    return { report: undefined, failure: { kind: "bounds_configuration", message: "symbols must be an object" } };
  }
  const dimensions = ["timeoutMs", "maxAttempts", "maxItems", "maxConcurrency"];
  const checker = program.getTypeChecker();
  function stablePath(fileName) { return path.relative(projectRoot, path.resolve(fileName)).split(path.sep).join("/"); }
  function resolveAlias(symbol) { return symbol && symbol.flags & ts.SymbolFlags.Alias ? checker.getAliasedSymbol(symbol) : symbol; }
  function isCallable(node) { return ts.isFunctionDeclaration(node) || ts.isMethodDeclaration(node) || ts.isGetAccessorDeclaration(node) || ts.isSetAccessorDeclaration(node) || ts.isConstructorDeclaration(node) || ts.isFunctionExpression(node) || ts.isArrowFunction(node); }
  function nameNode(node) {
    if (node.name) return node.name;
    if (ts.isConstructorDeclaration(node)) return node.parent.name ?? node;
    const parent = node.parent;
    if ((ts.isVariableDeclaration(parent) || ts.isPropertyDeclaration(parent) || ts.isPropertyAssignment(parent)) && parent.name) return parent.name;
    return undefined;
  }
  const aliasMap = new Map();
  for (const description of descriptions) for (const alias of description.aliases) {
    const values = aliasMap.get(alias) ?? [];
    values.push(description.canonicalName);
    aliasMap.set(alias, values);
  }
  const limits = new Map();
  for (const [key, value] of Object.entries(contractFile.symbols)) {
    const candidates = descriptions.filter((description) => description.canonicalName === key).map((description) => description.canonicalName);
    for (const candidate of aliasMap.get(key) ?? []) if (!candidates.includes(candidate)) candidates.push(candidate);
    if (candidates.length !== 1) return { report: undefined, failure: { kind: "bounds_configuration", message: `contract key ${key} is ${candidates.length === 0 ? "unknown" : "ambiguous"}` } };
    if (!value || typeof value !== "object" || Array.isArray(value)) return { report: undefined, failure: { kind: "bounds_configuration", message: `contract ${key} must be an object` } };
    const bounds = {};
    for (const [dimension, amount] of Object.entries(value)) {
      if (!dimensions.includes(dimension) || !Number.isSafeInteger(amount) || amount < 0) return { report: undefined, failure: { kind: "bounds_configuration", message: `contract ${key}.${dimension} must be a non-negative safe integer` } };
      bounds[dimension] = amount;
    }
    limits.set(candidates[0], bounds);
  }

  const graphByName = new Map(graph.map((node) => [node.symbol, node]));
  const graphByLocation = new Map(graph.map((node) => [`${node.location.path}\0${node.location.range.start.offset}`, node.symbol]));
  const callByLocation = new Map();
  for (const node of graph) {
    for (const call of node.calls ?? []) {
      for (const provenance of call.provenance ?? []) {
        callByLocation.set(`${provenance.path}\0${provenance.range.start.offset}`, call.target);
      }
    }
  }
  const declarationToCanonical = new Map();
  const nodeByCanonical = new Map();
  function discover(node) {
    if (isCallable(node) && node.body) {
      const named = nameNode(node);
      if (named) {
        const canonical = graphByLocation.get(`${stablePath(node.getSourceFile().fileName)}\0${named.getStart(node.getSourceFile())}`);
        const symbol = resolveAlias(checker.getSymbolAtLocation(named));
        if (canonical && symbol) { declarationToCanonical.set(symbol, canonical); nodeByCanonical.set(canonical, node); }
      }
    }
    ts.forEachChild(node, discover);
  }
  for (const sourceFile of program.getSourceFiles()) discover(sourceFile);
  function target(call) {
    const direct = callByLocation.get(`${stablePath(call.getSourceFile().fileName)}\0${call.getStart(call.getSourceFile())}`);
    if (direct) return direct;
    const signature = checker.getResolvedSignature(call);
    const declaration = signature?.declaration;
    const named = declaration && nameNode(declaration);
    const symbol = named && resolveAlias(checker.getSymbolAtLocation(named));
    return symbol && declarationToCanonical.get(symbol);
  }
  function empty() { return { bounds: { timeoutMs: 0, maxAttempts: 0, maxItems: 0, maxConcurrency: 0 }, unknown: [] }; }
  function sequential(left, right, node) {
    const result = empty();
    for (const dimension of dimensions) {
      const amount = dimension === "maxConcurrency" ? Math.max(left.bounds[dimension], right.bounds[dimension]) : left.bounds[dimension] + right.bounds[dimension];
      if (!Number.isSafeInteger(amount)) {
        result.bounds[dimension] = Number.MAX_SAFE_INTEGER;
        if (node) result.unknown.push(unknown(node, "arithmetic_overflow"));
      } else result.bounds[dimension] = amount;
    }
    result.unknown.push(...left.unknown, ...right.unknown);
    return result;
  }
  function exclusive(left, right) {
    const result = empty();
    for (const dimension of dimensions) result.bounds[dimension] = Math.max(left.bounds[dimension], right.bounds[dimension]);
    result.unknown = [...left.unknown, ...right.unknown];
    return result;
  }
  function concurrent(values, node) {
    const result = empty();
    for (const value of values) {
      result.bounds.timeoutMs = Math.max(result.bounds.timeoutMs, value.bounds.timeoutMs);
      result.bounds.maxAttempts += value.bounds.maxAttempts;
      result.bounds.maxItems += value.bounds.maxItems;
      result.bounds.maxConcurrency += Math.max(1, value.bounds.maxConcurrency);
      result.unknown.push(...value.unknown);
    }
    for (const dimension of dimensions) if (!Number.isSafeInteger(result.bounds[dimension])) {
      result.bounds[dimension] = Number.MAX_SAFE_INTEGER;
      result.unknown.push(unknown(node, "arithmetic_overflow"));
    }
    return result;
  }
  function multiply(value, count, node) {
    const result = empty();
    for (const dimension of dimensions) {
      const amount = dimension === "maxConcurrency" ? value.bounds[dimension] : value.bounds[dimension] * count;
      if (!Number.isSafeInteger(amount)) {
        result.bounds[dimension] = Number.MAX_SAFE_INTEGER;
        result.unknown.push(unknown(node, "arithmetic_overflow"));
      } else result.bounds[dimension] = amount;
    }
    result.unknown.push(...value.unknown);
    return result;
  }
  function unknown(node, reason) {
    const sourceFile = node.getSourceFile();
    const start = sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile));
    return { reason, path: stablePath(sourceFile.fileName), line: start.line + 1, column: start.character + 1 };
  }
  const recursive = new Set();
  function reaches(start, current, seen) {
    if (seen.has(current)) return false;
    seen.add(current);
    for (const edge of graphByName.get(current)?.calls ?? []) {
      if (edge.target === start || reaches(start, edge.target, seen)) return true;
    }
    return false;
  }
  for (const name of graphByName.keys()) if (reaches(name, name, new Set())) recursive.add(name);

  const current = new Map();
  for (const [name, node] of graphByName) {
    const description = descriptions.find((value) => value.canonicalName === name);
    if (description?.package && limits.has(name)) current.set(name, { bounds: { ...empty().bounds, ...limits.get(name) }, unknown: [] });
    else current.set(name, empty());
  }
  function loopCount(node) {
    if (!node.initializer || !ts.isVariableDeclarationList(node.initializer) || node.initializer.declarations.length !== 1 ||
        !node.condition || !ts.isBinaryExpression(node.condition) || !node.incrementor) return undefined;
    const declaration = node.initializer.declarations[0];
    if (!ts.isIdentifier(declaration.name) || !declaration.initializer || !ts.isNumericLiteral(declaration.initializer) ||
        !ts.isIdentifier(node.condition.left) || node.condition.left.text !== declaration.name.text || !ts.isNumericLiteral(node.condition.right)) return undefined;
    const start = Number(declaration.initializer.text), limit = Number(node.condition.right.text);
    let step;
    if ((ts.isPostfixUnaryExpression(node.incrementor) || ts.isPrefixUnaryExpression(node.incrementor)) &&
        ts.isIdentifier(node.incrementor.operand) && node.incrementor.operand.text === declaration.name.text) {
      step = node.incrementor.operator === ts.SyntaxKind.PlusPlusToken ? 1 : node.incrementor.operator === ts.SyntaxKind.MinusMinusToken ? -1 : undefined;
    } else if (ts.isBinaryExpression(node.incrementor) && ts.isIdentifier(node.incrementor.left) && node.incrementor.left.text === declaration.name.text && ts.isNumericLiteral(node.incrementor.right)) {
      const amount = Number(node.incrementor.right.text);
      step = node.incrementor.operatorToken.kind === ts.SyntaxKind.PlusEqualsToken ? amount :
        node.incrementor.operatorToken.kind === ts.SyntaxKind.MinusEqualsToken ? -amount : undefined;
    }
    if (!Number.isFinite(step) || step === 0) return undefined;
    const operator = node.condition.operatorToken.kind;
    if (step > 0 && (operator === ts.SyntaxKind.LessThanToken || operator === ts.SyntaxKind.LessThanEqualsToken)) {
      if (start > limit || start === limit && operator === ts.SyntaxKind.LessThanToken) return 0;
      return operator === ts.SyntaxKind.LessThanEqualsToken ? Math.floor((limit - start) / step) + 1 : Math.max(0, Math.ceil((limit - start) / step));
    }
    if (step < 0 && (operator === ts.SyntaxKind.GreaterThanToken || operator === ts.SyntaxKind.GreaterThanEqualsToken)) {
      if (start < limit || start === limit && operator === ts.SyntaxKind.GreaterThanToken) return 0;
      const distance = start - limit, amount = -step;
      return operator === ts.SyntaxKind.GreaterThanEqualsToken ? Math.floor(distance / amount) + 1 : Math.max(0, Math.ceil(distance / amount));
    }
    return undefined;
  }

  function evaluate(node, owner) {
    if (!node) return empty();
    if (ts.isCallExpression(node) || ts.isNewExpression(node)) {
      if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression) &&
          ts.isIdentifier(node.expression.expression) && node.expression.expression.text === "Promise" &&
          ["all", "allSettled", "any", "race"].includes(node.expression.name.text) &&
          node.arguments.length > 0 && ts.isArrayLiteralExpression(node.arguments[0])) {
        return concurrent(node.arguments[0].elements.map((element) => evaluate(ts.isSpreadElement(element) ? element.expression : element, owner)), node);
      }
      const name = target(node);
      const callValue = !name ? { ...empty(), unknown: [unknown(node, "unresolved_call")] } :
        recursive.has(name) ? { ...empty(), unknown: [unknown(node, "recursive_cycle")] } :
          current.get(name) ?? { ...empty(), unknown: [unknown(node, "missing_contract")] };
      let argumentsValue = empty();
      for (const argument of node.arguments ?? []) argumentsValue = sequential(argumentsValue, evaluate(argument, owner), node);
      return sequential(argumentsValue, callValue, node);
    }
    if (ts.isIfStatement(node)) return sequential(evaluate(node.expression, owner), exclusive(evaluate(node.thenStatement, owner), evaluate(node.elseStatement, owner)), node);
    if (ts.isConditionalExpression(node)) return sequential(evaluate(node.condition, owner), exclusive(evaluate(node.whenTrue, owner), evaluate(node.whenFalse, owner)), node);
    if (ts.isForStatement(node)) {
      const count = loopCount(node);
      if (!Number.isSafeInteger(count) || count < 0) return { ...empty(), unknown: [unknown(node, "unbounded_loop")] };
      return multiply(evaluate(node.statement, owner), count, node);
    }
    if (ts.isForOfStatement(node)) {
      if (ts.isArrayLiteralExpression(node.expression)) return multiply(evaluate(node.statement, owner), node.expression.elements.length, node);
      return { ...empty(), unknown: [unknown(node, "unbounded_loop")] };
    }
    if (ts.isWhileStatement(node) || ts.isDoStatement(node) || ts.isForInStatement(node)) return { ...empty(), unknown: [unknown(node, "unbounded_loop")] };
    let result = empty();
    ts.forEachChild(node, (child) => { result = sequential(result, evaluate(child, owner), node); });
    return result;
  }
  for (let pass = 0; pass < graph.length + 1; pass++) {
    let changed = false;
    for (const [name, node] of nodeByCanonical) {
      const description = descriptions.find((value) => value.canonicalName === name);
      if (description?.package && limits.has(name)) continue;
      if (recursive.has(name)) { current.set(name, { ...empty(), unknown: [unknown(node, "recursive_cycle")] }); continue; }
      const next = evaluate(node.body, name);
      const graphNode = graphByName.get(name);
      for (const leaf of graphNode?.unresolved ?? []) {
        if (["Promise.all", "Promise.allSettled", "Promise.any", "Promise.race"].includes(leaf.symbol)) continue;
        next.unknown.push({ reason: leaf.reason, path: leaf.provenance?.[0]?.path ?? graphNode.location.path, line: leaf.provenance?.[0]?.range?.start?.line ?? graphNode.location.range.start.line, column: leaf.provenance?.[0]?.range?.start?.column ?? graphNode.location.range.start.column });
      }
      const before = JSON.stringify(current.get(name));
      const after = JSON.stringify(next);
      if (before !== after) { current.set(name, next); changed = true; }
    }
    if (!changed) break;
  }
  const results = [];
  const violations = [];
  for (const [symbol, limit] of limits) {
    const value = current.get(symbol) ?? { ...empty(), unknown: [{ reason: "missing_summary", path: "", line: 0, column: 0 }] };
    const deduped = new Map(value.unknown.map((item) => [`${item.reason}\0${item.path}\0${item.line}\0${item.column}`, item]));
    const result = { symbol, bounds: value.bounds, limits: limit, unknown: [...deduped.values()] };
    results.push(result);
    for (const dimension of dimensions) if (limit[dimension] !== undefined && value.bounds[dimension] > limit[dimension]) violations.push({ symbol, dimension, actual: value.bounds[dimension], limit: limit[dimension] });
  }
  results.sort((left, right) => left.symbol < right.symbol ? -1 : left.symbol > right.symbol ? 1 : 0);
  violations.sort((left, right) => left.symbol < right.symbol ? -1 : left.symbol > right.symbol ? 1 : left.dimension < right.dimension ? -1 : 1);
  const report = { results, violations };
  if (results.some((result) => result.unknown.some((item) => item.reason === "arithmetic_overflow"))) {
    return { report, failure: { kind: "bounds_overflow", message: "resource-bound arithmetic exceeded Number.MAX_SAFE_INTEGER" } };
  }
  return { report };
}
