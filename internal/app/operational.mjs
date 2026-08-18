function analyzeOperational(program, projectRoot, ts) {
  const checker = program.getTypeChecker();
  const records = [];
  const byDeclaration = new Map();
  const bySymbol = new Map();

  function stableSourcePath(fileName) {
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

  function isCallable(node) {
    return (
      ts.isFunctionDeclaration(node) ||
      ts.isMethodDeclaration(node) ||
      ts.isGetAccessorDeclaration(node) ||
      ts.isSetAccessorDeclaration(node) ||
      ts.isConstructorDeclaration(node) ||
      ts.isFunctionExpression(node) ||
      ts.isArrowFunction(node)
    );
  }

  function bindingName(node) {
    if (node.name) {
      return node.name.getText(node.getSourceFile());
    }
    if (ts.isConstructorDeclaration(node)) {
      return "constructor";
    }
    const parent = node.parent;
    if ((ts.isVariableDeclaration(parent) || ts.isPropertyDeclaration(parent) || ts.isPropertyAssignment(parent)) && parent.name) {
      return parent.name.getText(node.getSourceFile());
    }
    if (ts.isExportAssignment(parent)) {
      return "default";
    }
    return undefined;
  }

  function symbolName(node) {
    const parts = [];
    let current = node;
    while (current && !ts.isSourceFile(current)) {
      if (isCallable(current)) {
        const name = bindingName(current);
        if (name) {
          parts.unshift(name);
        }
      } else if ((ts.isClassDeclaration(current) || ts.isClassExpression(current)) && current.name) {
        parts.unshift(current.name.text);
      } else if (
        ts.isVariableDeclaration(current) &&
        current.name &&
        current.initializer &&
        (ts.isObjectLiteralExpression(current.initializer) || ts.isClassExpression(current.initializer))
      ) {
        parts.unshift(current.name.getText(current.getSourceFile()));
      }
      current = current.parent;
    }
    if (parts.length === 0) {
      return undefined;
    }
    return `${stableSourcePath(node.getSourceFile().fileName)}::${parts.join(".")}`;
  }

  function nameNode(node) {
    if (node.name) {
      return node.name;
    }
    if (ts.isConstructorDeclaration(node)) {
      return node.parent.name;
    }
    const parent = node.parent;
    if ((ts.isVariableDeclaration(parent) || ts.isPropertyDeclaration(parent) || ts.isPropertyAssignment(parent)) && parent.name) {
      return parent.name;
    }
    return undefined;
  }

  function discover(node) {
    if (isCallable(node) && node.body) {
      const symbol = symbolName(node);
      if (symbol) {
        const namedNode = nameNode(node) ?? node;
        const record = {
          node,
          symbol,
          execution: executionFor(node),
          location: {
            path: stableSourcePath(node.getSourceFile().fileName),
            range: sourceRange(namedNode),
          },
          errors: [],
          effects: [],
          unresolved: [],
          calls: [],
        };
        records.push(record);
        byDeclaration.set(node, record);
        const typeScriptSymbol = checker.getSymbolAtLocation(namedNode);
        if (typeScriptSymbol) {
          bySymbol.set(resolveAlias(typeScriptSymbol), record);
        }
      }
    }
    ts.forEachChild(node, discover);
  }

  function executionFor(node) {
    if (node.modifiers?.some((modifier) => modifier.kind === ts.SyntaxKind.AsyncKeyword)) {
      return "asynchronous";
    }
    const signature = checker.getSignatureFromDeclaration(node);
    if (signature && checker.getPromisedTypeOfPromise(checker.getReturnTypeOfSignature(signature))) {
      return "asynchronous";
    }
    return "synchronous";
  }

  function resolveAlias(symbol) {
    return symbol.flags & ts.SymbolFlags.Alias ? checker.getAliasedSymbol(symbol) : symbol;
  }

  function provenance(record, node) {
    return [{ symbol: record.symbol, path: record.location.path, range: sourceRange(node) }];
  }

  function symbolAtCall(call) {
    const location = ts.isPropertyAccessExpression(call.expression) ? call.expression.name : call.expression;
    const symbol = checker.getSymbolAtLocation(location);
    return symbol ? resolveAlias(symbol) : undefined;
  }

  function localTarget(call) {
    const signature = checker.getResolvedSignature(call);
    if (signature?.declaration && byDeclaration.has(signature.declaration)) {
      return byDeclaration.get(signature.declaration);
    }
    const symbol = symbolAtCall(call);
    return symbol ? bySymbol.get(symbol) : undefined;
  }

  function declarationFiles(symbol) {
    return (symbol?.declarations ?? []).map((declaration) => declaration.getSourceFile().fileName.split(path.sep).join("/"));
  }

  function isExternalSymbol(symbol) {
    if (!symbol) {
      return false;
    }
    const declarations = resolveAlias(symbol).declarations ?? [];
    return declarations.length > 0 && declarations.every((declaration) => declaration.getSourceFile().isDeclarationFile);
  }

  function receiverIsExternal(expression) {
    const symbol = checker.getSymbolAtLocation(expression);
    return isExternalSymbol(symbol);
  }

  function modeledEffect(call) {
    const callee = call.expression;
    const symbol = symbolAtCall(call);
    if (ts.isIdentifier(callee)) {
      if (callee.text === "fetch" && isExternalSymbol(symbol)) return "network";
      if (["setTimeout", "setInterval"].includes(callee.text) && isExternalSymbol(symbol)) return "time";
      if (["alert", "confirm", "prompt"].includes(callee.text) && isExternalSymbol(symbol)) return "io";
      if (callee.text === "WebSocket" && isExternalSymbol(symbol)) return "network";
    }
    if (ts.isNewExpression(call) && ts.isIdentifier(callee)) {
      if (callee.text === "Date" && isExternalSymbol(symbol)) return "time";
      if (callee.text === "WebSocket" && isExternalSymbol(symbol)) return "network";
    }
    if (!ts.isPropertyAccessExpression(callee)) {
      return undefined;
    }
    const receiver = callee.expression.getText(callee.getSourceFile());
    const method = callee.name.text;
    if (receiver === "console" && ["debug", "error", "info", "log", "trace", "warn"].includes(method) && receiverIsExternal(callee.expression)) return "io";
    if (receiver === "Math" && method === "random" && receiverIsExternal(callee.expression)) return "random";
    if (receiver === "Date" && method === "now" && receiverIsExternal(callee.expression)) return "time";
    if (receiver === "performance" && method === "now" && receiverIsExternal(callee.expression)) return "time";
    if (receiver === "crypto" && ["getRandomValues", "randomUUID"].includes(method) && receiverIsExternal(callee.expression)) return "random";
    if (["localStorage", "sessionStorage"].includes(receiver) && ["clear", "getItem", "key", "removeItem", "setItem"].includes(method) && receiverIsExternal(callee.expression)) return "state";
    if (receiver === "indexedDB" && ["cmp", "databases", "deleteDatabase", "open"].includes(method) && receiverIsExternal(callee.expression)) return "database";
    if (receiver === "process" && ["abort", "chdir", "exit", "kill"].includes(method) && receiverIsExternal(callee.expression)) return "process";
    const files = declarationFiles(symbol);
    if (files.some((fileName) => /\/(@types\/node\/)?fs(?:\/promises)?\.d\.ts$/.test(fileName))) return "filesystem";
    return undefined;
  }

  function propertyEffect(node) {
    if (!ts.isPropertyAccessExpression(node)) {
      return undefined;
    }
    if (node.getText(node.getSourceFile()) === "process.env" && receiverIsExternal(node.expression)) {
      return "environment";
    }
    return undefined;
  }

  function isErrorType(type, seen = new Set()) {
    if (!type || seen.has(type)) {
      return false;
    }
    seen.add(type);
    if (type.symbol?.name === "Error") {
      return true;
    }
    return (type.getBaseTypes?.() ?? []).some((base) => isErrorType(base, seen));
  }

  function concreteError(expression) {
    if (!ts.isNewExpression(expression)) {
      return undefined;
    }
    const type = checker.getTypeAtLocation(expression);
    if (!isErrorType(type)) {
      return undefined;
    }
    return type.aliasSymbol?.name ?? type.symbol?.name ?? expression.expression.getText(expression.getSourceFile());
  }

  function catchVariable(node) {
    let current = node.parent;
    while (current) {
      if (ts.isCatchClause(current)) {
        const declaration = current.variableDeclaration;
        return declaration && ts.isIdentifier(declaration.name) ? declaration.name.text : undefined;
      }
      if (isCallable(current)) {
        return undefined;
      }
      current = current.parent;
    }
    return undefined;
  }

  function instanceOfType(expression, variable) {
    while (ts.isParenthesizedExpression(expression)) {
      expression = expression.expression;
    }
    if (
      !ts.isBinaryExpression(expression) ||
      expression.operatorToken.kind !== ts.SyntaxKind.InstanceOfKeyword ||
      !ts.isIdentifier(expression.left) ||
      expression.left.text !== variable
    ) {
      return undefined;
    }
    const type = checker.getTypeAtLocation(expression.right);
    return type.symbol?.name ?? expression.right.getText(expression.getSourceFile());
  }

  function containsRethrow(statement, variable) {
    let found = false;
    function visit(node) {
      if (node !== statement && isCallable(node)) return;
      if (ts.isThrowStatement(node) && ts.isIdentifier(node.expression) && node.expression.text === variable) {
        found = true;
        return;
      }
      ts.forEachChild(node, visit);
    }
    visit(statement);
    return found;
  }

  function terminates(statement) {
    if (ts.isReturnStatement(statement) || ts.isThrowStatement(statement)) return true;
    if (ts.isBlock(statement)) {
      return statement.statements.length > 0 && terminates(statement.statements[statement.statements.length - 1]);
    }
    if (ts.isIfStatement(statement) && statement.elseStatement) {
      return terminates(statement.thenStatement) && terminates(statement.elseStatement);
    }
    return false;
  }

  function guardedRethrow(throwStatement, catchClause, variable) {
    let child = throwStatement;
    let current = throwStatement.parent;
    while (current && current !== catchClause.block) {
      if (ts.isIfStatement(current)) {
        const name = instanceOfType(current.expression, variable);
        if (name) {
          if (child === current.thenStatement) return { mode: "only", name };
          if (child === current.elseStatement) return { mode: "except", name };
        }
      }
      child = current;
      current = current.parent;
    }
    return undefined;
  }

  function policyForCatch(catchClause) {
    const declaration = catchClause.variableDeclaration;
    if (!declaration || !ts.isIdentifier(declaration.name)) {
      return { mode: "none" };
    }
    const variable = declaration.name.text;
    const handled = new Set();
    const only = new Set();
    let rethrowRest = false;

    function visit(node) {
      if (node !== catchClause.block && isCallable(node)) return;
      if (ts.isIfStatement(node)) {
        const name = instanceOfType(node.expression, variable);
        if (name && terminates(node.thenStatement) && !containsRethrow(node.thenStatement, variable)) {
          handled.add(name);
        }
      }
      if (ts.isThrowStatement(node) && ts.isIdentifier(node.expression) && node.expression.text === variable) {
        const guard = guardedRethrow(node, catchClause, variable);
        if (!guard) {
          rethrowRest = true;
        } else if (guard.mode === "only") {
          only.add(guard.name);
        } else {
          handled.add(guard.name);
          rethrowRest = true;
        }
      }
      ts.forEachChild(node, visit);
    }
    visit(catchClause.block);

    if (rethrowRest) {
      return handled.size > 0 ? { mode: "except", names: [...handled].sort() } : undefined;
    }
    if (only.size > 0) {
      return { mode: "only", names: [...only].sort() };
    }
    return { mode: "none" };
  }

  function policiesFor(node, callable) {
    const policies = [];
    let child = node;
    let current = node.parent;
    while (current && current !== callable.node) {
      if (ts.isTryStatement(current) && child === current.tryBlock && current.catchClause) {
        const policy = policyForCatch(current.catchClause);
        if (policy) policies.push(policy);
      }
      child = current;
      current = current.parent;
    }
    return policies;
  }

  function unresolvedCall(call, record) {
    const symbol = symbolAtCall(call);
    const declarations = symbol?.declarations ?? [];
    const reason = declarations.length > 0 && declarations.every((declaration) => declaration.getSourceFile().isDeclarationFile)
      ? "declaration_only"
      : "unmodeled_call";
    return {
      symbol: call.expression.getText(call.getSourceFile()),
      reason,
      provenance: provenance(record, call),
    };
  }

  function analyze(record) {
    function visit(node) {
      if (node !== record.node.body && isCallable(node)) {
        return;
      }
      if (ts.isThrowStatement(node)) {
        const name = concreteError(node.expression);
        if (name) {
          record.errors.push({ name, provenance: provenance(record, node), policies: policiesFor(node, record) });
        } else if (!(ts.isIdentifier(node.expression) && node.expression.text === catchVariable(node))) {
          record.unresolved.push({
            symbol: node.expression.getText(node.getSourceFile()),
            reason: "non_concrete_throw",
            provenance: provenance(record, node),
          });
        }
      }
      if (ts.isCallExpression(node) || ts.isNewExpression(node)) {
        if (!(ts.isNewExpression(node) && ts.isThrowStatement(node.parent))) {
          const target = localTarget(node);
          if (target) {
            record.calls.push({
              target: target.symbol,
              provenance: provenance(record, node),
              policies: policiesFor(node, record),
            });
          } else {
            const effect = modeledEffect(node);
            if (effect) {
              record.effects.push({ name: effect, provenance: provenance(record, node) });
            } else {
              record.unresolved.push(unresolvedCall(node, record));
            }
          }
        }
      } else {
        const effect = propertyEffect(node);
        if (effect) {
          record.effects.push({ name: effect, provenance: provenance(record, node) });
        }
      }
      ts.forEachChild(node, visit);
    }
    visit(record.node.body);
  }

  for (const sourceFile of program.getSourceFiles()) {
    const relative = path.relative(projectRoot, path.resolve(sourceFile.fileName));
    if (
      !sourceFile.isDeclarationFile &&
      relative !== "" &&
      relative !== ".." &&
      !relative.startsWith(`..${path.sep}`) &&
      !relative.split(path.sep).includes("node_modules")
    ) {
      discover(sourceFile);
    }
  }
  for (const record of records) {
    analyze(record);
  }
  return records.map(({ node: _node, ...record }) => record);
}
