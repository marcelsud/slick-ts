function analyzeRiskInputs(program, projectRoot, ts, collected, crap) {
  if (process.env.SLICK_RISK !== "1") return [];
  const checker = program.getTypeChecker();
  function stablePath(fileName) { return path.relative(projectRoot, path.resolve(fileName)).split(path.sep).join("/"); }
  function authored(sourceFile) {
    const relative = path.relative(projectRoot, path.resolve(sourceFile.fileName));
    return !sourceFile.isDeclarationFile && relative !== "" && relative !== ".." && !relative.startsWith(`..${path.sep}`) && !relative.split(path.sep).includes("node_modules");
  }
  const sources = program.getSourceFiles().filter(authored);
  const byAbsolute = new Map(sources.map((sourceFile) => [path.resolve(sourceFile.fileName), sourceFile]));
  const incoming = new Map(sources.map((sourceFile) => [stablePath(sourceFile.fileName), new Set()]));
  function target(moduleSpecifier) {
    const symbol = checker.getSymbolAtLocation(moduleSpecifier);
    const resolved = symbol && symbol.flags & ts.SymbolFlags.Alias ? checker.getAliasedSymbol(symbol) : symbol;
    const declaration = resolved?.declarations?.find((value) => byAbsolute.has(path.resolve(value.getSourceFile().fileName)));
    return declaration?.getSourceFile();
  }
  for (const sourceFile of sources) {
    function visit(node) {
      let specifier;
      if (ts.isImportDeclaration(node) && ts.isStringLiteral(node.moduleSpecifier) && !node.importClause?.isTypeOnly) specifier = node.moduleSpecifier;
      if (ts.isExportDeclaration(node) && node.moduleSpecifier && ts.isStringLiteral(node.moduleSpecifier) && !node.isTypeOnly) specifier = node.moduleSpecifier;
      if (specifier) {
        const destination = target(specifier);
        if (destination) incoming.get(stablePath(destination.fileName))?.add(stablePath(sourceFile.fileName));
      }
      ts.forEachChild(node, visit);
    }
    visit(sourceFile);
  }
  const coverageBySymbol = new Map((crap?.results ?? []).map((value) => [value.symbol, value.coverage]));
  const results = [];
  for (const functions of collected.functionsByFile.values()) {
    for (const callable of functions) {
      results.push({
        symbol: callable.symbol,
        path: callable.path,
        range: callable.range,
        complexity: callable.complexity,
        coverage: coverageBySymbol.has(callable.symbol) ? coverageBySymbol.get(callable.symbol) : null,
        fanIn: incoming.get(callable.path)?.size ?? 0,
      });
    }
  }
  results.sort((left, right) => left.path < right.path ? -1 : left.path > right.path ? 1 : left.range.start.offset - right.range.start.offset);
  return results;
}
