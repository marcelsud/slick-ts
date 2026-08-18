function analyzeOperational(program, projectRoot, ts) {
  const checker = program.getTypeChecker();
  const records = [];
  const byDeclaration = new Map();
  const bySymbol = new Map();
  const identifiers = new Set();

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
      const name = node.name.getText(node.getSourceFile());
      if (ts.isGetAccessorDeclaration(node)) return `get:${name}`;
      if (ts.isSetAccessorDeclaration(node)) return `set:${name}`;
      return name;
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

  function lexicalName(node, ownName) {
    const parts = [ownName];
    let current = node.parent;
    while (current && !ts.isSourceFile(current)) {
      if (isCallable(current)) {
        const name = bindingName(current);
        if (name) parts.unshift(name);
      } else if ((ts.isClassDeclaration(current) || ts.isClassExpression(current)) && current.name) {
        parts.unshift(current.name.text);
      } else if (ts.isModuleDeclaration(current)) {
        parts.unshift(current.name.getText(current.getSourceFile()));
      } else if (
        ts.isVariableDeclaration(current) &&
        current.name &&
        current.initializer &&
        (ts.isObjectLiteralExpression(current.initializer) || ts.isClassExpression(current.initializer))
      ) {
        parts.unshift(current.name.getText(current.getSourceFile()));
      } else if (
        (ts.isBlock(current) && !(isCallable(current.parent) && current.parent.body === current)) ||
        ts.isCaseClause(current) ||
        ts.isDefaultClause(current)
      ) {
        parts.unshift(`scope@${current.getStart(current.getSourceFile())}`);
      }
      current = current.parent;
    }
    return `${stableSourcePath(node.getSourceFile().fileName)}::${parts.join(".")}`;
  }

  function symbolName(node) {
    const ownName = bindingName(node);
    return ownName ? lexicalName(node, ownName) : undefined;
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
        if (identifiers.has(symbol)) {
          throw new Error(`duplicate operational symbol ${symbol}`);
        }
        identifiers.add(symbol);
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
      if (callee.text === "Date" && ts.isCallExpression(call) && isExternalSymbol(symbol)) return "time";
    }
    if (ts.isNewExpression(call) && ts.isIdentifier(callee)) {
      if (callee.text === "Date" && isExternalSymbol(symbol)) return (call.arguments?.length ?? 0) === 0 ? "time" : "pure";
      if (callee.text === "WebSocket" && isExternalSymbol(symbol)) return "network";
    }
    const files = declarationFiles(symbol);
    if (files.some((fileName) => /\/node_modules\/@types\/node\/fs(?:\/promises)?\.d\.ts$/.test(fileName))) return "filesystem";
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
    return undefined;
  }

  function propertyEffect(node) {
    if (!ts.isPropertyAccessExpression(node)) {
      return undefined;
    }
    if (
      node.name.text === "env" &&
      ts.isIdentifier(node.expression) &&
      node.expression.text === "process" &&
      receiverIsExternal(node.expression)
    ) {
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

  function typeIdentity(type) {
    const symbol = type.aliasSymbol ?? type.symbol;
    if (!symbol) return checker.typeToString(type);
    const declaration = symbol.valueDeclaration ?? symbol.declarations?.[0];
    if (declaration) {
      const relative = path.relative(projectRoot, path.resolve(declaration.getSourceFile().fileName));
      if (relative !== "" && relative !== ".." && !relative.startsWith(`..${path.sep}`)) {
        return lexicalName(declaration, symbol.name);
      }
    }
    return checker.getFullyQualifiedName(symbol).replace(/^\".*\"\\./, "");
  }

  function errorDetails(type) {
    const symbol = type.aliasSymbol ?? type.symbol;
    const supertypes = new Set();
    function collect(current) {
      for (const base of current.getBaseTypes?.() ?? []) {
        supertypes.add(typeIdentity(base));
        collect(base);
      }
    }
    collect(type);
    return {
      name: symbol?.name ?? checker.typeToString(type),
      type: typeIdentity(type),
      supertypes: [...supertypes].sort(),
    };
  }

  function concreteError(expression) {
    if (!ts.isNewExpression(expression)) {
      return undefined;
    }
    const type = checker.getTypeAtLocation(expression);
    return isErrorType(type) ? errorDetails(type) : undefined;
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
    let negated = false;
    while (ts.isParenthesizedExpression(expression) || ts.isPrefixUnaryExpression(expression)) {
      if (ts.isPrefixUnaryExpression(expression)) {
        if (expression.operator !== ts.SyntaxKind.ExclamationToken) return undefined;
        negated = !negated;
      }
      expression = expression.operand ?? expression.expression;
    }
    if (
      !ts.isBinaryExpression(expression) ||
      expression.operatorToken.kind !== ts.SyntaxKind.InstanceOfKeyword ||
      !ts.isIdentifier(expression.left) ||
      expression.left.text !== variable
    ) {
      return undefined;
    }
    const constructor = checker.getTypeAtLocation(expression.right);
    const signature = checker.getSignaturesOfType(constructor, ts.SignatureKind.Construct)[0];
    if (!signature) return undefined;
    const instance = checker.getReturnTypeOfSignature(signature);
    return { type: typeIdentity(instance), negated };
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
        const guard = instanceOfType(current.expression, variable);
        if (guard) {
          if (child === current.thenStatement) {
            return { mode: guard.negated ? "except" : "only", type: guard.type };
          }
          if (child === current.elseStatement) {
            return { mode: guard.negated ? "only" : "except", type: guard.type };
          }
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
    const unguardedOnly = new Set();
    let rethrowRest = false;

    function visit(node) {
      if (node !== catchClause.block && isCallable(node)) return;
      if (ts.isIfStatement(node)) {
        const guard = instanceOfType(node.expression, variable);
        if (guard && terminates(node.thenStatement) && !containsRethrow(node.thenStatement, variable)) {
          if (guard.negated) {
            unguardedOnly.add(guard.type);
          } else {
            handled.add(guard.type);
          }
        }
      }
      if (ts.isThrowStatement(node) && ts.isIdentifier(node.expression) && node.expression.text === variable) {
        const guard = guardedRethrow(node, catchClause, variable);
        if (!guard) {
          rethrowRest = true;
        } else if (guard.mode === "only") {
          only.add(guard.type);
        } else {
          handled.add(guard.type);
          rethrowRest = true;
        }
      }
      ts.forEachChild(node, visit);
    }
    visit(catchClause.block);

    if (rethrowRest) {
      if (unguardedOnly.size > 0) {
        return { mode: "only", types: [...unguardedOnly].sort() };
      }
      return handled.size > 0 ? { mode: "except", types: [...handled].sort() } : undefined;
    }
    if (only.size > 0) {
      return { mode: "only", types: [...only].sort() };
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

  function isDeclarationOnly(declaration) {
    if (declaration.getSourceFile().isDeclarationFile) return true;
    if (isCallable(declaration) && !declaration.body) return true;
    let current = declaration;
    while (current && !ts.isSourceFile(current)) {
      if (current.modifiers?.some((modifier) => modifier.kind === ts.SyntaxKind.DeclareKeyword)) {
        return true;
      }
      current = current.parent;
    }
    return false;
  }

  function unresolvedCall(call, record) {
    const symbol = symbolAtCall(call);
    const declarations = symbol?.declarations ?? [];
    const reason = declarations.length > 0 && declarations.every(isDeclarationOnly)
      ? "declaration_only"
      : "unmodeled_call";
    return {
      symbol: call.expression.getText(call.getSourceFile()),
      reason,
      provenance: provenance(record, call),
    };
  }

  function forwardsAsyncErrors(node, record) {
    let current = node;
    while (current.parent && current !== record.node) {
      const parent = current.parent;
      if (ts.isAwaitExpression(parent) || ts.isReturnStatement(parent) || ts.isYieldExpression(parent)) {
        return true;
      }
      if (ts.isArrowFunction(record.node) && record.node.body === current) {
        return true;
      }
      if (
        ts.isParenthesizedExpression(parent) ||
        ts.isAsExpression(parent) ||
        ts.isTypeAssertionExpression(parent) ||
        ts.isNonNullExpression(parent) ||
        ts.isSatisfiesExpression(parent)
      ) {
        current = parent;
        continue;
      }
      return false;
    }
    return false;
  }

  function accessorTargets(node) {
    if (!ts.isPropertyAccessExpression(node) && !ts.isElementAccessExpression(node)) {
      return [];
    }
    const location = ts.isPropertyAccessExpression(node) ? node.name : node.argumentExpression;
    const symbol = checker.getSymbolAtLocation(location) ?? checker.getSymbolAtLocation(node);
    if (!symbol) return [];
    let read = true;
    let write = false;
    const parent = node.parent;
    if (ts.isBinaryExpression(parent) && parent.left === node && parent.operatorToken.kind >= ts.SyntaxKind.FirstAssignment && parent.operatorToken.kind <= ts.SyntaxKind.LastAssignment) {
      write = true;
      read = parent.operatorToken.kind !== ts.SyntaxKind.EqualsToken;
    } else if (ts.isPrefixUnaryExpression(parent) || ts.isPostfixUnaryExpression(parent)) {
      read = true;
      write = true;
    }
    const targets = [];
    for (const declaration of resolveAlias(symbol).declarations ?? []) {
      if ((read && ts.isGetAccessorDeclaration(declaration)) || (write && ts.isSetAccessorDeclaration(declaration))) {
        const target = byDeclaration.get(declaration);
        if (target) targets.push(target);
      }
    }
    return targets;
  }

  function analyze(record) {
    function visit(node) {
      if (node !== record.node.body && isCallable(node)) {
        return;
      }
      if (ts.isThrowStatement(node)) {
        const error = concreteError(node.expression);
        if (error) {
          record.errors.push({
            name: error.name,
            type: error.type,
            supertypes: error.supertypes,
            provenance: provenance(record, node),
            policies: policiesFor(node, record),
          });
        } else if (!(ts.isIdentifier(node.expression) && node.expression.text === catchVariable(node))) {
          record.unresolved.push({
            symbol: node.expression.getText(node.getSourceFile()),
            reason: "non_concrete_throw",
            provenance: provenance(record, node),
          });
        }
      }
      if (ts.isCallExpression(node) || ts.isNewExpression(node)) {
        const concreteConstruction = ts.isNewExpression(node) && ts.isThrowStatement(node.parent) && concreteError(node);
        const target = localTarget(node);
        if (target) {
          record.calls.push({
            target: target.symbol,
            provenance: provenance(record, node),
            policies: policiesFor(node, record),
            propagateAsyncErrors: forwardsAsyncErrors(node, record),
          });
        } else {
          const effect = modeledEffect(node);
          if (effect && effect !== "pure") {
            record.effects.push({ name: effect, provenance: provenance(record, node) });
          } else if (effect !== "pure" && !concreteConstruction) {
            record.unresolved.push(unresolvedCall(node, record));
          }
        }
      } else if (ts.isPropertyAccessExpression(node) || ts.isElementAccessExpression(node)) {
        const targets = accessorTargets(node);
        for (const target of targets) {
          record.calls.push({
            target: target.symbol,
            provenance: provenance(record, node),
            policies: policiesFor(node, record),
            propagateAsyncErrors: forwardsAsyncErrors(node, record),
          });
        }
        if (targets.length === 0) {
          const effect = propertyEffect(node);
          if (effect) {
            record.effects.push({ name: effect, provenance: provenance(record, node) });
          }
        }
      }
      ts.forEachChild(node, visit);
    }
    for (const parameter of record.node.parameters ?? []) {
      if (parameter.initializer) visit(parameter.initializer);
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
