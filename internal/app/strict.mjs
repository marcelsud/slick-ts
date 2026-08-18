function analyzeStrict(program, projectRoot, ts, typeScriptDiagnostics) {
  const checker = program.getTypeChecker();
  const diagnostics = [];
  const seen = new Set();
  const rules = {
    any: {
      code: 1001,
      title: "Unsafe any",
      explanation: "A value has type any, so Slick cannot prove its contract.",
      repairs: ["Replace any with unknown and narrow it", "Add a concrete type at this boundary"],
    },
    assertion: {
      code: 1002,
      title: "Unchecked assertion",
      explanation: "An assertion can claim a stronger type without runtime evidence.",
      repairs: ["Use a control-flow guard before use", "Parse and validate the value at runtime"],
    },
    truthiness: {
      code: 1003,
      title: "Implicit truthiness",
      explanation: "Slick conditions require a boolean instead of implicit truthiness.",
      repairs: ["Compare the value explicitly", "Call a predicate that returns boolean"],
    },
    promise: {
      code: 1004,
      title: "Unconsumed Promise",
      explanation: "A Promise was created without an owner for its completion or rejection.",
      repairs: ["Await the Promise", "Return it to the caller", "Await or return Promise.all([...])", "Pass it to a modeled ownership transfer"],
    },
  };

  function stablePath(fileName) {
    return path.relative(projectRoot, path.resolve(fileName)).split(path.sep).join("/");
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

  function overlapsTypeScriptDiagnostic(node) {
    const sourceFile = node.getSourceFile();
    const start = node.getStart(sourceFile);
    const end = node.getEnd();
    return typeScriptDiagnostics.some((diagnostic) =>
      diagnostic.category === ts.DiagnosticCategory.Error &&
      diagnostic.file?.fileName === sourceFile.fileName &&
      diagnostic.start !== undefined &&
      diagnostic.start < end &&
      diagnostic.start + (diagnostic.length ?? 0) > start,
    );
  }

  function report(node, rule, fact) {
    if (overlapsTypeScriptDiagnostic(node)) return;
    const range = sourceRange(node);
    const fileName = stablePath(node.getSourceFile().fileName);
    const key = `${rule.code}\0${fileName}\0${range.start.offset}\0${range.end.offset}`;
    if (seen.has(key)) return;
    seen.add(key);
    diagnostics.push({
      source: "slick",
      code: rule.code,
      category: "error",
      title: rule.title,
      message: rule.explanation,
      explanation: rule.explanation,
      fact,
      repairs: rule.repairs,
      path: fileName,
      range,
    });
  }

  function isTypePosition(node) {
    let current = node;
    while (current.parent) {
      const parent = current.parent;
      if (ts.isTypeNode(parent)) return true;
      if (
        ts.isExpressionWithTypeArguments(parent) ||
        ts.isTypeParameterDeclaration(parent) ||
        ts.isImportTypeNode(parent)
      ) return true;
      if (ts.isExpression(parent) || ts.isStatement(parent) || ts.isDeclaration(parent)) break;
      current = parent;
    }
    return false;
  }

  function isDeclarationName(node) {
    const parent = node.parent;
    return (
      (ts.isDeclaration(parent) && parent.name === node) ||
      (ts.isPropertyAccessExpression(parent) && parent.name === node) ||
      (ts.isPropertyAssignment(parent) && parent.name === node) ||
      ts.isImportSpecifier(parent) ||
      ts.isExportSpecifier(parent) ||
      ts.isImportClause(parent) ||
      ts.isNamespaceImport(parent) ||
      ts.isLabeledStatement(parent) ||
      ts.isBreakOrContinueStatement(parent)
    );
  }

  function typeIsAny(node) {
    return Boolean(checker.getTypeAtLocation(node).flags & ts.TypeFlags.Any);
  }
  const anyOrigins = new Set();

  function containsAnyKeyword(node) {
    let found = false;
    function scan(current) {
      if (current.kind === ts.SyntaxKind.AnyKeyword) {
        found = true;
        return;
      }
      ts.forEachChild(current, scan);
    }
    if (node) scan(node);
    return found;
  }

  function collectAnyOrigins(node) {
    if (ts.isDeclaration(node) && node.name) {
      const symbol = symbolOf(node.name);
      if (
        containsAnyKeyword(node.type) ||
        (ts.isVariableDeclaration(node) && !node.type && node.initializer && typeIsAny(node.initializer)) ||
        (ts.isParameter(node) && !node.type && typeIsAny(node))
      ) {
        if (symbol) anyOrigins.add(symbol);
      }
    }
    ts.forEachChild(node, collectAnyOrigins);
  }


  function booleanType(type) {
    if (type.flags & ts.TypeFlags.Never) return true;
    if (type.flags & (ts.TypeFlags.Boolean | ts.TypeFlags.BooleanLiteral)) return true;
    if (type.isUnion?.()) return type.types.every(booleanType);
    const constraint = checker.getBaseConstraintOfType(type);
    return constraint && constraint !== type ? booleanType(constraint) : false;
  }

  function requireBoolean(expression) {
    const type = checker.getTypeAtLocation(expression);
    if (!booleanType(type)) {
      report(expression, rules.truthiness, `condition type: ${checker.typeToString(type)}`);
    }
  }

  function promiseType(node) {
    const type = checker.getTypeAtLocation(node);
    if (type.flags & (ts.TypeFlags.Any | ts.TypeFlags.Unknown)) return false;
    return checker.getPromisedTypeOfPromise(type) !== undefined;
  }

  function symbolOf(node) {
    return ts.isIdentifier(node) ? checker.getSymbolAtLocation(node) : undefined;
  }

  function isPromiseCombinator(call) {
    return ts.isPropertyAccessExpression(call.expression) &&
      ts.isIdentifier(call.expression.expression) &&
      call.expression.expression.text === "Promise" &&
      ["all", "allSettled", "any", "race"].includes(call.expression.name.text);
  }

  function isOwnershipTransfer(call) {
    if (!ts.isPropertyAccessExpression(call.expression)) return false;
    const name = call.expression.name.text;
    if (!["respondWith", "waitUntil"].includes(name)) return false;
    const symbol = checker.getSymbolAtLocation(call.expression.name);
    return (symbol?.declarations ?? []).some((declaration) => declaration.getSourceFile().isDeclarationFile);
  }

  function argumentConsumed(node, call) {
    if (isPromiseCombinator(call) || isOwnershipTransfer(call)) return true;
    return false;
  }

  function ignoredCallback(functionNode) {
    const call = functionNode.parent;
    if (!ts.isCallExpression(call) || !call.arguments.includes(functionNode)) return false;
    const contextual = checker.getContextualType(functionNode);
    const signature = contextual && checker.getSignaturesOfType(contextual, ts.SignatureKind.Call)[0];
    if (signature && checker.getReturnTypeOfSignature(signature).flags & ts.TypeFlags.Void) return true;
    if (
      ts.isPropertyAccessExpression(call.expression) &&
      ["map", "flatMap"].includes(call.expression.name.text)
    ) {
      if (ts.isCallExpression(call.parent) && call.parent.arguments.includes(call) && isPromiseCombinator(call.parent)) {
        return false;
      }
      const assigned = assignedSymbol(call);
      return !(assigned && assignedPromiseConsumed(call, assigned));
    }
    return false;
  }

  function enclosingCallbackReturnIgnored(node) {
    let current = node.parent;
    while (current) {
      if (ts.isArrowFunction(current) || ts.isFunctionExpression(current)) return ignoredCallback(current);
      if (ts.isFunctionDeclaration(current) || ts.isMethodDeclaration(current)) return false;
      current = current.parent;
    }
    return false;
  }

  const consumedSymbols = new Map();
  function collectPromiseValues(node) {
    while (
      ts.isParenthesizedExpression(node) ||
      ts.isAsExpression(node) ||
      ts.isTypeAssertionExpression(node) ||
      ts.isNonNullExpression(node)
    ) {
      node = node.expression;
    }
    if (ts.isIdentifier(node)) {
      const symbol = symbolOf(node);
      if (symbol) {
        const uses = consumedSymbols.get(symbol) ?? [];
        uses.push(node);
        consumedSymbols.set(symbol, uses);
      }
    } else if (ts.isArrayLiteralExpression(node)) {
      for (const element of node.elements) collectPromiseValues(ts.isSpreadElement(element) ? element.expression : element);
    }
  }

  function discoverConsumption(node) {
    if (ts.isAwaitExpression(node)) {
      collectPromiseValues(node.expression);
    } else if (ts.isReturnStatement(node) && node.expression && !enclosingCallbackReturnIgnored(node)) {
      collectPromiseValues(node.expression);
    } else if (ts.isCallExpression(node) && (isPromiseCombinator(node) || isOwnershipTransfer(node))) {
      for (const argument of node.arguments) collectPromiseValues(argument);
    } else if (
      ts.isCallExpression(node) &&
      ts.isPropertyAccessExpression(node.expression) &&
      ["then", "catch", "finally"].includes(node.expression.name.text)
    ) {
      collectPromiseValues(node.expression.expression);
    }
    ts.forEachChild(node, discoverConsumption);
  }

  function assignedSymbol(expression) {
    const parent = expression.parent;
    if (ts.isVariableDeclaration(parent) && parent.initializer === expression) return symbolOf(parent.name);
    if (ts.isBinaryExpression(parent) && parent.right === expression && parent.operatorToken.kind === ts.SyntaxKind.EqualsToken) {
      return symbolOf(parent.left);
    }
    return undefined;
  }
  function conditionalAncestors(node) {
    const result = new Set();
    let child = node;
    let current = node.parent;
    while (current) {
      if (
        (ts.isIfStatement(current) && (child === current.thenStatement || child === current.elseStatement)) ||
        (ts.isConditionalExpression(current) && (child === current.whenTrue || child === current.whenFalse)) ||
        ((ts.isForStatement(current) || ts.isForInStatement(current) || ts.isForOfStatement(current) ||
          ts.isWhileStatement(current) || ts.isDoStatement(current)) && child === current.statement) ||
        ts.isCaseClause(current) ||
        ts.isDefaultClause(current)
      ) {
        result.add(child);
      }
      if (ts.isFunctionDeclaration(current) || ts.isMethodDeclaration(current) ||
          ts.isArrowFunction(current) || ts.isFunctionExpression(current)) break;
      child = current;
      current = current.parent;
    }
    return result;
  }

  function assignedPromiseConsumed(expression, symbol) {
    const start = expression.getStart();
    const creationConditions = conditionalAncestors(expression);
    let nextAssignment = Number.POSITIVE_INFINITY;
    function findNext(node) {
      if (node !== expression) {
        if (
          (ts.isVariableDeclaration(node) && node.initializer && symbolOf(node.name) === symbol) ||
          (ts.isBinaryExpression(node) && node.operatorToken.kind === ts.SyntaxKind.EqualsToken &&
            symbolOf(node.left) === symbol)
        ) {
          const position = node.getStart();
          const conditions = conditionalAncestors(node);
          const definitelyOverwrites = [...conditions].every((condition) => creationConditions.has(condition));
          if (position > start && definitelyOverwrites) {
            nextAssignment = Math.min(nextAssignment, position);
          }
        }
      }
      ts.forEachChild(node, findNext);
    }
    findNext(expression.getSourceFile());
    return (consumedSymbols.get(symbol) ?? []).some((use) => {
      const position = use.getStart();
      if (position <= start || position >= nextAssignment) return false;
      for (const condition of conditionalAncestors(use)) {
        if (!creationConditions.has(condition)) return false;
      }
      return true;
    });
  }

  function expressionConsumed(expression) {
    const assigned = assignedSymbol(expression);
    if (assigned && assignedPromiseConsumed(expression, assigned)) return true;
    let current = expression;
    for (;;) {
      const parent = current.parent;
      if (!parent) return false;
      if (ts.isAwaitExpression(parent) && parent.expression === current) return true;
      if (ts.isReturnStatement(parent) && parent.expression === current) {
        return !enclosingCallbackReturnIgnored(parent);
      }
      if (ts.isArrowFunction(parent) && parent.body === current) return !ignoredCallback(parent);
      if (ts.isCallExpression(parent) && parent.arguments.includes(current)) return argumentConsumed(current, parent);
      if (
        ts.isPropertyAccessExpression(parent) && parent.expression === current &&
        ts.isCallExpression(parent.parent) && parent.parent.expression === parent &&
        ["then", "catch", "finally"].includes(parent.name.text)
      ) return true;
      if (
        ts.isParenthesizedExpression(parent) ||
        ts.isAsExpression(parent) ||
        ts.isTypeAssertionExpression(parent) ||
        ts.isNonNullExpression(parent) ||
        ts.isArrayLiteralExpression(parent)
      ) {
        current = parent;
        continue;
      }
      return false;
    }
  }

  function visit(node) {
    if (node.kind === ts.SyntaxKind.AnyKeyword) {
      report(node, rules.any, "explicit type: any");
    }

    if (ts.isVariableDeclaration(node) && !node.type && node.initializer && typeIsAny(node.initializer)) {
      report(node.name, rules.any, "inferred type: any");
    } else if (ts.isParameter(node) && !node.type && typeIsAny(node)) {
      report(node.name, rules.any, "inferred parameter type: any");
    } else if (
      ts.isIdentifier(node) &&
      !isDeclarationName(node) &&
      !isTypePosition(node) &&
      typeIsAny(node) &&
      !anyOrigins.has(symbolOf(node))
    ) {
      report(node, rules.any, "authored value flow type: any");
    } else if (
      (ts.isCallExpression(node) || ts.isPropertyAccessExpression(node) || ts.isElementAccessExpression(node)) &&
      typeIsAny(node) &&
      !(ts.isVariableDeclaration(node.parent) && node.parent.initializer === node)
    ) {
      report(node, rules.any, "authored value flow type: any");
    }

    if (ts.isAsExpression(node) || ts.isTypeAssertionExpression(node)) {
      const source = checker.getTypeAtLocation(node.expression);
      const target = checker.getTypeFromTypeNode(node.type);
      if (!checker.isTypeAssignableTo(source, target)) {
        report(node, rules.assertion, `assertion: ${checker.typeToString(source)} to ${checker.typeToString(target)}`);
      }
    } else if (ts.isNonNullExpression(node)) {
      const source = checker.getTypeAtLocation(node.expression);
      const target = checker.getTypeAtLocation(node);
      if (!checker.isTypeAssignableTo(source, target)) {
        report(node, rules.assertion, `non-null assertion: ${checker.typeToString(source)} to ${checker.typeToString(target)}`);
      }
    }

    if (ts.isIfStatement(node) || ts.isWhileStatement(node) || ts.isDoStatement(node) || ts.isConditionalExpression(node)) {
      requireBoolean(node.expression ?? node.condition);
    } else if (ts.isForStatement(node) && node.condition) {
      requireBoolean(node.condition);
    } else if (
      ts.isBinaryExpression(node) &&
      [ts.SyntaxKind.AmpersandAmpersandToken, ts.SyntaxKind.BarBarToken].includes(node.operatorToken.kind)
    ) {
      requireBoolean(node.left);
    } else if (ts.isPrefixUnaryExpression(node) && node.operator === ts.SyntaxKind.ExclamationToken) {
      requireBoolean(node.operand);
    }

    if ((ts.isCallExpression(node) || ts.isNewExpression(node)) && promiseType(node) && !expressionConsumed(node)) {
      report(node, rules.promise, `unowned Promise expression: ${node.expression.getText(node.getSourceFile())}`);
    }
    if (
      (ts.isArrowFunction(node) || ts.isFunctionExpression(node)) &&
      node.modifiers?.some((modifier) => modifier.kind === ts.SyntaxKind.AsyncKeyword) &&
      ignoredCallback(node)
    ) {
      report(node, rules.promise, "async callback result is discarded by a void callback contract");
    }

    ts.forEachChild(node, visit);
  }

  const syntacticErrorFiles = new Set(
    program.getSyntacticDiagnostics()
      .filter((diagnostic) => diagnostic.category === ts.DiagnosticCategory.Error && diagnostic.file)
      .map((diagnostic) => path.resolve(diagnostic.file.fileName)),
  );
  const authored = program.getSourceFiles().filter((sourceFile) => {
    const absolute = path.resolve(sourceFile.fileName);
    const relative = path.relative(projectRoot, absolute);
    return !sourceFile.isDeclarationFile && !syntacticErrorFiles.has(absolute) &&
      relative !== "" && relative !== ".." && !relative.startsWith(`..${path.sep}`) &&
      !relative.split(path.sep).includes("node_modules");
  });
  for (const sourceFile of authored) collectAnyOrigins(sourceFile);
  for (const sourceFile of authored) discoverConsumption(sourceFile);
  for (const sourceFile of authored) visit(sourceFile);
  return diagnostics;
}
