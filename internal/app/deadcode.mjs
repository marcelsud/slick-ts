function analyzeDeadCode(program, projectRoot, ts, descriptions, entryPaths) {
  if (process.env.SLICK_DEAD_CODE !== "1") return { report: undefined };
  const checker = program.getTypeChecker();

  function stablePath(fileName) {
    return path.relative(projectRoot, path.resolve(fileName)).split(path.sep).join("/");
  }

  function resolveAlias(symbol) {
    return symbol && symbol.flags & ts.SymbolFlags.Alias ? checker.getAliasedSymbol(symbol) : symbol;
  }

  function nameNode(node) {
    if (node.name) return node.name;
    if (ts.isConstructorDeclaration(node)) return node.parent.name ?? node;
    const parent = node.parent;
    if ((ts.isVariableDeclaration(parent) || ts.isPropertyDeclaration(parent) || ts.isPropertyAssignment(parent)) && parent.name) return parent.name;
    return undefined;
  }

  function isCallable(node) {
    return ts.isFunctionDeclaration(node) || ts.isMethodDeclaration(node) || ts.isGetAccessorDeclaration(node) ||
      ts.isSetAccessorDeclaration(node) || ts.isConstructorDeclaration(node) || ts.isFunctionExpression(node) || ts.isArrowFunction(node);
  }

  const authored = program.getSourceFiles().filter((sourceFile) => {
    const relative = path.relative(projectRoot, path.resolve(sourceFile.fileName));
    return !sourceFile.isDeclarationFile && relative !== "" && relative !== ".." &&
      !relative.startsWith(`..${path.sep}`) && !relative.split(path.sep).includes("node_modules");
  });
  const authoredByPath = new Map(authored.map((sourceFile) => [path.resolve(sourceFile.fileName), sourceFile]));
  let entries = (entryPaths ?? []).map((value) => path.resolve(value));
  if (entries.length === 0) {
    const targets = [];
    function collectTargets(value) {
      if (typeof value === "string") targets.push(value);
      else if (Array.isArray(value)) value.forEach(collectTargets);
      else if (value && typeof value === "object") Object.values(value).forEach(collectTargets);
    }
    try {
      const packageJSON = JSON.parse(fs.readFileSync(path.join(projectRoot, "package.json"), "utf8"));
      collectTargets(packageJSON.exports ?? packageJSON.module ?? packageJSON.main ?? packageJSON.bin);
    } catch {}
    const options = program.getCompilerOptions();
    const rootDir = path.resolve(options.rootDir ?? program.getCommonSourceDirectory());
    const outDir = options.outDir ? path.resolve(options.outDir) : undefined;
    function sourceCandidates(target) {
      const absolute = path.resolve(projectRoot, target);
      const extension = path.extname(absolute);
      const sourceExtensions = extension === ".mjs" ? [".mts"] : extension === ".cjs" ? [".cts"] : [".ts", ".tsx"];
      const candidates = [absolute];
      if (outDir) {
        const relative = path.relative(outDir, absolute);
        if (relative !== ".." && !relative.startsWith(`..${path.sep}`)) {
          for (const sourceExtension of sourceExtensions) candidates.push(path.join(rootDir, relative.slice(0, -extension.length) + sourceExtension));
        }
      }
      const normalized = path.relative(projectRoot, absolute).split(path.sep);
      if (normalized[0] === "dist") {
        for (const sourceExtension of sourceExtensions) candidates.push(path.join(projectRoot, "src", ...normalized.slice(1)).slice(0, -extension.length) + sourceExtension);
      }
      return candidates;
    }
    for (const target of targets) {
      for (const absolute of sourceCandidates(target)) {
        if (authoredByPath.has(absolute) && !entries.includes(absolute)) entries.push(absolute);
      }
    }
    if (entries.length === 0) {
      const indexes = authored.filter((sourceFile) => ["index.ts", "index.tsx", "index.mts", "index.cts"].includes(path.basename(sourceFile.fileName)));
      entries = indexes.map((sourceFile) => path.resolve(sourceFile.fileName));
    }
    if (entries.length === 0) return { report: undefined, failure: { kind: "entry_configuration", message: "dead-code analysis needs --entry or an authored package export" } };
  }
  for (const entry of entries) {
    if (!authoredByPath.has(entry)) return { report: undefined, failure: { kind: "entry_configuration", message: `entry ${stablePath(entry)} is not an authored source file` } };
  }

  const descriptionsByLocation = new Map(descriptions.map((description) => [
    `${description.location.path}\0${description.location.range.start.offset}`,
    description,
  ]));
  const records = [];
  const bySymbol = new Map();
  const byCanonical = new Map();
  function discover(node) {
    const named = (isCallable(node) || ts.isClassDeclaration(node) || ts.isClassExpression(node)) ? nameNode(node) : undefined;
    if (named) {
      const sourceFile = node.getSourceFile();
      const symbol = resolveAlias(checker.getSymbolAtLocation(named));
      let description = descriptionsByLocation.get(`${stablePath(sourceFile.fileName)}\0${named.getStart(sourceFile)}`);
      if (!description && symbol) {
        const startOffset = named.getStart(sourceFile), endOffset = named.getEnd();
        const start = sourceFile.getLineAndCharacterOfPosition(startOffset), end = sourceFile.getLineAndCharacterOfPosition(endOffset);
        description = {
          canonicalName: `${stablePath(sourceFile.fileName)}::${named.getText(sourceFile)}`,
          kind: ts.isClassDeclaration(node) || ts.isClassExpression(node) ? "class" : "function",
          visibility: "local",
          location: { path: stablePath(sourceFile.fileName), range: { start: { line: start.line + 1, column: start.character + 1, offset: startOffset }, end: { line: end.line + 1, column: end.character + 1, offset: endOffset } } },
        };
      }
      if (description && symbol && !byCanonical.has(description.canonicalName)) {
        const record = { node, symbol, description, edges: new Set(), unknownDynamic: false };
        records.push(record);
        bySymbol.set(symbol, record);
        byCanonical.set(description.canonicalName, record);
      }
    }
    ts.forEachChild(node, discover);
  }
  for (const sourceFile of authored) discover(sourceFile);

  function targetAt(node) {
    return bySymbol.get(resolveAlias(checker.getSymbolAtLocation(node)));
  }

  function collectEdges(record) {
    function visit(node) {
      if (node !== record.node && isCallable(node)) {
        const nested = nameNode(node);
        const target = nested && targetAt(nested);
        if (target) record.edges.add(target);
        return;
      }
      if (ts.isCallExpression(node) && node.expression.kind === ts.SyntaxKind.ImportKeyword &&
          (node.arguments.length !== 1 || !ts.isStringLiteral(node.arguments[0]))) {
        record.unknownDynamic = true;
      }
      if (ts.isIdentifier(node) || ts.isPropertyAccessExpression(node) || ts.isNewExpression(node)) {
        const location = ts.isPropertyAccessExpression(node) ? node.name : ts.isNewExpression(node) ? node.expression : node;
        const target = targetAt(location);
        if (target && target !== record) record.edges.add(target);
      }
      ts.forEachChild(node, visit);
    }
    visit(record.node);
  }
  for (const record of records) collectEdges(record);

  const moduleImports = new Map(authored.map((sourceFile) => [path.resolve(sourceFile.fileName), new Set()]));
  const moduleUnknown = new Set();
  function importedSource(specifier) {
    const symbol = checker.getSymbolAtLocation(specifier);
    const resolved = resolveAlias(symbol);
    const declaration = resolved?.declarations?.find((value) => authoredByPath.has(path.resolve(value.getSourceFile().fileName)));
    return declaration?.getSourceFile();
  }
  for (const sourceFile of authored) {
    function imports(node) {
      let specifier;
      let runtime = true;
      if (ts.isImportDeclaration(node) && ts.isStringLiteral(node.moduleSpecifier)) {
        specifier = node.moduleSpecifier;
        runtime = !node.importClause?.isTypeOnly;
      } else if (ts.isExportDeclaration(node) && node.moduleSpecifier && ts.isStringLiteral(node.moduleSpecifier)) {
        specifier = node.moduleSpecifier;
        runtime = !node.isTypeOnly;
      } else if (ts.isCallExpression(node) && node.expression.kind === ts.SyntaxKind.ImportKeyword) {
        if (node.arguments.length === 1 && ts.isStringLiteral(node.arguments[0])) specifier = node.arguments[0];
        else moduleUnknown.add(path.resolve(sourceFile.fileName));
      }
      if (specifier && runtime) {
        const target = importedSource(specifier);
        if (target) moduleImports.get(path.resolve(sourceFile.fileName)).add(path.resolve(target.fileName));
      }
      ts.forEachChild(node, imports);
    }
    imports(sourceFile);
  }
  const reachableModules = new Set();
  const moduleQueue = [...entries];
  while (moduleQueue.length > 0) {
    const module = moduleQueue.shift();
    if (reachableModules.has(module)) continue;
    reachableModules.add(module);
    for (const target of moduleImports.get(module) ?? []) moduleQueue.push(target);
  }
  const roots = new Set();
  for (const entry of reachableModules) {
    const sourceFile = authoredByPath.get(entry);
    const isPublicEntry = entries.includes(entry);
    const moduleSymbol = sourceFile.symbol;
    if (isPublicEntry) {
      for (const exported of moduleSymbol ? checker.getExportsOfModule(moduleSymbol) : []) {
        const target = bySymbol.get(resolveAlias(exported));
        if (target) {
          roots.add(target);
          if (target.description.kind === "class") {
            const prefix = `${target.description.canonicalName}.`;
            for (const candidate of records) if (candidate.description.canonicalName.startsWith(prefix)) roots.add(candidate);
          }
        }
      }
    }
    function topLevel(node) {
      if (isCallable(node) || ts.isClassDeclaration(node)) return;
      if (ts.isIdentifier(node) && node.parent?.name === node &&
          (ts.isVariableDeclaration(node.parent) || ts.isFunctionDeclaration(node.parent) ||
           ts.isClassDeclaration(node.parent) || ts.isMethodDeclaration(node.parent) ||
           ts.isParameter(node.parent))) return;
      if (ts.isIdentifier(node) || ts.isPropertyAccessExpression(node) || ts.isNewExpression(node)) {
        const location = ts.isPropertyAccessExpression(node) ? node.name : ts.isNewExpression(node) ? node.expression : node;
        const target = targetAt(location);
        if (target) roots.add(target);
      }
      ts.forEachChild(node, topLevel);
    }
    topLevel(sourceFile);
  }

  const reachable = new Set();
  const queue = [...roots];
  while (queue.length > 0) {
    const record = queue.shift();
    if (reachable.has(record)) continue;
    reachable.add(record);
    for (const edge of record.edges) queue.push(edge);
  }

  const hasUnknownDynamicTarget = [...reachable].some((record) => record.unknownDynamic) ||
    [...reachableModules].some((module) => moduleUnknown.has(module));
  const unknown = hasUnknownDynamicTarget
    ? [{ reason: "dynamic_import_target", message: "a reachable dynamic import target is not statically known" }]
    : [];
  const unreachable = hasUnknownDynamicTarget ? [] : records
    .filter((record) => !reachable.has(record))
    .map((record) => ({
      symbol: record.description.canonicalName,
      kind: record.description.kind,
      path: record.description.location.path,
      range: record.description.location.range,
      module: record.description.location.path,
    }));
  unreachable.sort((left, right) => left.path < right.path ? -1 : left.path > right.path ? 1 : left.range.start.offset - right.range.start.offset);
  return { report: { entries: entries.map(stablePath).sort(), unreachable, unknown } };
}
