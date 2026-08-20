function analyzeArchitecture(program, projectRoot, ts, configPath) {
  if (process.env.SLICK_ARCHITECTURE !== "1") return { report: undefined };
  let config;
  try {
    config = JSON.parse(fs.readFileSync(configPath, "utf8"));
  } catch (error) {
    return { report: undefined, failure: { kind: "architecture_configuration", message: error instanceof Error ? error.message : String(error) } };
  }
  if (!config || !Array.isArray(config.layers)) {
    return { report: undefined, failure: { kind: "architecture_configuration", message: "layers must be an array" } };
  }
  const checker = program.getTypeChecker();
  function stablePath(fileName) {
    return path.relative(projectRoot, path.resolve(fileName)).split(path.sep).join("/");
  }
  function authored(sourceFile) {
    const relative = path.relative(projectRoot, path.resolve(sourceFile.fileName));
    return !sourceFile.isDeclarationFile && relative !== "" && relative !== ".." &&
      !relative.startsWith(`..${path.sep}`) && !relative.split(path.sep).includes("node_modules");
  }
  function glob(pattern) {
    let result = "^";
    for (let index = 0; index < pattern.length; index++) {
      const character = pattern[index];
      if (character === "*" && pattern[index + 1] === "*") {
        result += ".*";
        index++;
      } else if (character === "*") result += "[^/]*";
      else result += character.replace(/[|\\{}()[\]^$+?.]/g, "\\$&");
    }
    return new RegExp(result + "$");
  }
  const layers = [];
  const layerNames = new Set();
  for (const layer of config.layers) {
    if (!layer || typeof layer.name !== "string" || !Array.isArray(layer.include) || !Array.isArray(layer.mayImport) || layerNames.has(layer.name)) {
      return { report: undefined, failure: { kind: "architecture_configuration", message: "each layer needs a unique name, include array, and mayImport array" } };
    }
    layerNames.add(layer.name);
    layers.push({ ...layer, patterns: layer.include.map(glob) });
  }
  for (const layer of layers) {
    for (const allowed of layer.mayImport) if (!layerNames.has(allowed)) {
      return { report: undefined, failure: { kind: "architecture_configuration", message: `layer ${layer.name} allows unknown layer ${allowed}` } };
    }
  }
  const maxFanIn = config.maxFanIn ?? 0;
  const maxFanOut = config.maxFanOut ?? 0;
  if (!Number.isInteger(maxFanIn) || maxFanIn < 0 || !Number.isInteger(maxFanOut) || maxFanOut < 0) {
    return { report: undefined, failure: { kind: "architecture_configuration", message: "maxFanIn and maxFanOut must be non-negative integers" } };
  }

  const sources = program.getSourceFiles().filter(authored);
  const byPath = new Map(sources.map((sourceFile) => [path.resolve(sourceFile.fileName), sourceFile]));
  const modules = [];
  const moduleByPath = new Map();
  for (const sourceFile of sources) {
    const modulePath = stablePath(sourceFile.fileName);
    const matches = layers.filter((layer) => layer.patterns.some((pattern) => pattern.test(modulePath)));
    if (matches.length > 1) return { report: undefined, failure: { kind: "architecture_configuration", message: `${modulePath} matches multiple layers` } };
    const module = { path: modulePath, layer: matches[0]?.name ?? "", fanIn: 0, fanOut: 0 };
    modules.push(module);
    moduleByPath.set(path.resolve(sourceFile.fileName), module);
  }

  function targetFile(moduleSpecifier) {
    const symbol = checker.getSymbolAtLocation(moduleSpecifier);
    const resolved = symbol && symbol.flags & ts.SymbolFlags.Alias ? checker.getAliasedSymbol(symbol) : symbol;
    const declaration = resolved?.declarations?.find((value) => byPath.has(path.resolve(value.getSourceFile().fileName))) ?? resolved?.declarations?.[0];
    const sourceFile = declaration?.getSourceFile();
    return sourceFile && byPath.has(path.resolve(sourceFile.fileName)) ? sourceFile : undefined;
  }
  function isTypeOnlyImport(node) {
    if (node.importClause?.isTypeOnly) return true;
    const bindings = node.importClause?.namedBindings;
    return bindings && ts.isNamedImports(bindings) && bindings.elements.length > 0 && bindings.elements.every((element) => element.isTypeOnly);
  }
  const edges = [];
  const unresolved = [];
  for (const sourceFile of sources) {
    function visit(node) {
      let specifier;
      let typeOnly = false;
      let kind = "import";
      if (ts.isImportDeclaration(node) && ts.isStringLiteral(node.moduleSpecifier)) {
        specifier = node.moduleSpecifier;
        typeOnly = isTypeOnlyImport(node);
      } else if (ts.isExportDeclaration(node) && node.moduleSpecifier && ts.isStringLiteral(node.moduleSpecifier)) {
        specifier = node.moduleSpecifier;
        typeOnly = node.isTypeOnly;
        kind = "export";
      } else if (ts.isImportEqualsDeclaration(node) && ts.isExternalModuleReference(node.moduleReference) && ts.isStringLiteral(node.moduleReference.expression)) {
        specifier = node.moduleReference.expression;
        typeOnly = node.isTypeOnly;
      } else if (ts.isCallExpression(node) && node.expression.kind === ts.SyntaxKind.ImportKeyword) {
        kind = "dynamic-import";
        if (node.arguments.length === 1 && ts.isStringLiteral(node.arguments[0])) specifier = node.arguments[0];
        else unresolved.push({ source: stablePath(sourceFile.fileName), reason: "dynamic_import_target", line: sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile)).line + 1 });
      }
      if (specifier) {
        const target = targetFile(specifier);
        if (target) {
          const start = sourceFile.getLineAndCharacterOfPosition(specifier.getStart(sourceFile));
          edges.push({ source: stablePath(sourceFile.fileName), target: stablePath(target.fileName), typeOnly, kind, line: start.line + 1, column: start.character + 1 });
        }
      }
      ts.forEachChild(node, visit);
    }
    visit(sourceFile);
  }
  const edgeKeys = new Set();
  const uniqueEdges = [];
  for (const edge of edges) {
    const key = `${edge.source}\0${edge.target}\0${edge.typeOnly}\0${edge.kind}`;
    if (!edgeKeys.has(key)) { edgeKeys.add(key); uniqueEdges.push(edge); }
  }
  uniqueEdges.sort((left, right) => left.source < right.source ? -1 : left.source > right.source ? 1 : left.target < right.target ? -1 : left.target > right.target ? 1 : Number(left.typeOnly) - Number(right.typeOnly));

  const incoming = new Map(modules.map((module) => [module.path, new Set()]));
  const outgoing = new Map(modules.map((module) => [module.path, new Set()]));
  for (const edge of uniqueEdges) {
    outgoing.get(edge.source)?.add(edge.target);
    incoming.get(edge.target)?.add(edge.source);
  }
  for (const module of modules) {
    module.fanIn = incoming.get(module.path).size;
    module.fanOut = outgoing.get(module.path).size;
  }
  modules.sort((left, right) => left.path < right.path ? -1 : left.path > right.path ? 1 : 0);

  const violations = [];
  const moduleMap = new Map(modules.map((module) => [module.path, module]));
  for (const edge of uniqueEdges) {
    const source = moduleMap.get(edge.source);
    const target = moduleMap.get(edge.target);
    if (source?.layer && target?.layer && source.layer !== target.layer) {
      const sourceLayer = layers.find((layer) => layer.name === source.layer);
      if (!sourceLayer.mayImport.includes(target.layer)) violations.push({ kind: "layer", source: edge.source, target: edge.target, sourceLayer: source.layer, targetLayer: target.layer, line: edge.line, column: edge.column });
    }
  }
  for (const module of modules) {
    if (maxFanIn > 0 && module.fanIn > maxFanIn) violations.push({ kind: "fan_in", path: module.path, actual: module.fanIn, limit: maxFanIn });
    if (maxFanOut > 0 && module.fanOut > maxFanOut) violations.push({ kind: "fan_out", path: module.path, actual: module.fanOut, limit: maxFanOut });
  }

  const includeTypeOnly = config.allowTypeOnlyCycles !== true;
  const adjacency = new Map(modules.map((module) => [module.path, []]));
  for (const edge of uniqueEdges) if (includeTypeOnly || !edge.typeOnly) adjacency.get(edge.source)?.push(edge.target);
  for (const values of adjacency.values()) values.sort();
  let index = 0;
  const stack = [];
  const onStack = new Set();
  const state = new Map();
  const cycles = [];
  function connect(node) {
    const current = { index, low: index };
    index++;
    state.set(node, current);
    stack.push(node);
    onStack.add(node);
    for (const target of adjacency.get(node) ?? []) {
      if (!state.has(target)) { connect(target); current.low = Math.min(current.low, state.get(target).low); }
      else if (onStack.has(target)) current.low = Math.min(current.low, state.get(target).index);
    }
    if (current.low === current.index) {
      const component = [];
      let value;
      do { value = stack.pop(); onStack.delete(value); component.push(value); } while (value !== node);
      component.sort();
      if (component.length > 1 || (adjacency.get(node) ?? []).includes(node)) cycles.push({ modules: component });
    }
  }
  for (const module of modules) if (!state.has(module.path)) connect(module.path);
  cycles.sort((left, right) => left.modules.join("\0") < right.modules.join("\0") ? -1 : 1);
  return { report: { modules, edges: uniqueEdges, cycles, violations, unresolved } };
}
