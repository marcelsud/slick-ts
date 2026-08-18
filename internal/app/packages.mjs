const analysisSchemaVersion = 1;

function packageNameFromSpecifier(specifier) {
  const parts = specifier.split("/");
  return specifier.startsWith("@") ? parts.slice(0, 2).join("/") : parts[0];
}

function packageExportFromSpecifier(specifier, packageName) {
  const suffix = specifier.slice(packageName.length);
  return suffix ? `.${suffix}` : ".";
}

function runtimeImports(sourceFile, ts) {
  const imports = [];
  function visit(node) {
    if (ts.isImportDeclaration(node) && !node.importClause?.isTypeOnly && ts.isStringLiteral(node.moduleSpecifier)) {
      imports.push({ specifier: node.moduleSpecifier.text, kind: "import" });
    } else if (ts.isExportDeclaration(node) && !node.isTypeOnly && node.moduleSpecifier && ts.isStringLiteral(node.moduleSpecifier)) {
      imports.push({ specifier: node.moduleSpecifier.text, kind: "import" });
    } else if (
      ts.isCallExpression(node) &&
      node.arguments.length === 1 &&
      ts.isStringLiteral(node.arguments[0])
    ) {
      if (node.expression.kind === ts.SyntaxKind.ImportKeyword) {
        imports.push({ specifier: node.arguments[0].text, kind: "import" });
      } else if (ts.isIdentifier(node.expression) && node.expression.text === "require") {
        imports.push({ specifier: node.arguments[0].text, kind: "require" });
      }
    }
    ts.forEachChild(node, visit);
  }
  visit(sourceFile);
  return imports;
}

function resolvePackageImplementations(program, parsed, projectRoot, ts) {
  const records = [];
  const recordsByKey = new Map();
  const implementationFiles = new Set();
  const pendingSources = [];
  const scannedSources = new Set();
  const activeConditions = new Set(["node", "default", ...(parsed.options.customConditions ?? [])]);

  function stablePath(fileName) {
    return path.relative(projectRoot, path.resolve(fileName)).split(path.sep).join("/");
  }

  function packageRoot(start, packageName) {
    let current = path.dirname(start);
    for (;;) {
      const packagePath = path.join(current, "package.json");
      try {
        const value = JSON.parse(fs.readFileSync(packagePath, "utf8"));
        if (value.name === packageName) return { root: current, path: packagePath, value };
      } catch {}
      const parent = path.dirname(current);
      if (parent === current) return undefined;
      current = parent;
    }
  }

  function selectTarget(value, conditions, selected = []) {
    if (typeof value === "string") return { target: value, conditions: selected };
    if (Array.isArray(value)) {
      for (const candidate of value) {
        const result = selectTarget(candidate, conditions, selected);
        if (result) return result;
      }
      return undefined;
    }
    if (!value || typeof value !== "object") return undefined;
    for (const [condition, target] of Object.entries(value)) {
      if (condition === "types" || condition === "typings") continue;
      if (condition === "default" || conditions.has(condition)) {
        const result = selectTarget(target, conditions, [...selected, condition]);
        if (result) return result;
      }
    }
    return undefined;
  }

  function exportedTarget(packageJSON, exportPath, kind) {
    const conditions = new Set(activeConditions);
    conditions.add(kind);
    const exports = packageJSON.exports;
    if (exports !== undefined) {
      const target = exports && typeof exports === "object" && !Array.isArray(exports) &&
          Object.keys(exports).some((key) => key.startsWith("."))
        ? exports[exportPath]
        : exportPath === "." ? exports : undefined;
      return selectTarget(target, conditions);
    }
    if (exportPath !== ".") return undefined;
    const target = packageJSON.main ?? "index.js";
    return { target, conditions: ["main"] };
  }

  function existingSource(target) {
    const candidates = [target];
    if (!path.extname(target)) {
      candidates.push(...[".js", ".mjs", ".cjs", ".ts", ".mts", ".cts"].map((extension) => target + extension));
      candidates.push(...["index.js", "index.mjs", "index.cjs", "index.ts"].map((name) => path.join(target, name)));
    }
    for (const candidate of candidates) {
      try {
        if (fs.statSync(candidate).isFile()) return path.resolve(candidate);
      } catch {}
    }
    return path.resolve(target);
  }

  function declarationFor(specifier, containingFile) {
    return ts.resolveModuleName(specifier, containingFile, parsed.options, ts.sys).resolvedModule?.resolvedFileName;
  }

  function addSource(record, fileName) {
    const resolved = path.resolve(fileName);
    if (record.sourceSet.has(resolved)) return;
    record.sourceSet.add(resolved);
    implementationFiles.add(resolved);
    pendingSources.push({ record, fileName: resolved });
  }

  function addPackage(specifier, containingFile, kind) {
    const name = packageNameFromSpecifier(specifier);
    if (!name || name === "." || name === "..") return;
    const declarationFile = declarationFor(specifier, containingFile);
    let packageInfo = declarationFile ? packageRoot(declarationFile, name) : undefined;
    if (!packageInfo) {
      try {
        const implementation = createRequire(containingFile).resolve(specifier);
        packageInfo = packageRoot(implementation, name);
      } catch {}
    }
    if (!packageInfo) return;

    const exportPath = packageExportFromSpecifier(specifier, name);
    const key = `${packageInfo.root}\0${exportPath}\0${kind}`;
    const existing = recordsByKey.get(key);
    if (existing) return existing;

    const selected = exportedTarget(packageInfo.value, exportPath, kind);
    const unresolvedTarget = selected?.target ? path.resolve(packageInfo.root, selected.target) : undefined;
    const implementationFile = unresolvedTarget ? existingSource(unresolvedTarget) : undefined;
    const extension = implementationFile ? path.extname(implementationFile) : "";
    const sourceAvailable = implementationFile && ![".d.ts", ".d.mts", ".d.cts", ".node"].some((suffix) => implementationFile.endsWith(suffix));
    const reason = extension === ".node" ? "native_addon" : sourceAvailable ? undefined : "declaration_only";
    const record = {
      name,
      version: String(packageInfo.value.version ?? "0.0.0"),
      exportPath,
      conditions: selected?.conditions ?? [],
      packageRoot: packageInfo.root,
      packagePath: packageInfo.path,
      declarationFile: declarationFile ? path.resolve(declarationFile) : undefined,
      implementationFile,
      reason,
      sourceSet: new Set(),
    };
    recordsByKey.set(key, record);
    records.push(record);
    if (sourceAvailable) addSource(record, implementationFile);
    return record;
  }

  for (const sourceFile of program.getSourceFiles()) {
    const relative = path.relative(projectRoot, path.resolve(sourceFile.fileName));
    if (
      sourceFile.isDeclarationFile ||
      relative === "" ||
      relative === ".." ||
      relative.startsWith(`..${path.sep}`) ||
      relative.split(path.sep).includes("node_modules")
    ) continue;
    for (const imported of runtimeImports(sourceFile, ts)) {
      if (!imported.specifier.startsWith(".") && !path.isAbsolute(imported.specifier)) {
        addPackage(imported.specifier, sourceFile.fileName, imported.kind);
      }
    }
  }

  while (pendingSources.length > 0) {
    const { record, fileName } = pendingSources.shift();
    const scanKey = `${record.packageRoot}\0${fileName}`;
    if (scannedSources.has(scanKey)) continue;
    scannedSources.add(scanKey);
    let source;
    try {
      source = fs.readFileSync(fileName, "utf8");
    } catch {
      continue;
    }
    const sourceFile = ts.createSourceFile(fileName, source, parsed.options.target ?? ts.ScriptTarget.ES2022, true);
    for (const imported of runtimeImports(sourceFile, ts)) {
      if (imported.specifier.startsWith(".") || path.isAbsolute(imported.specifier)) {
        try {
          const resolved = createRequire(fileName).resolve(imported.specifier);
          if (resolved.startsWith(record.packageRoot + path.sep)) addSource(record, resolved);
        } catch {}
      } else if (!imported.specifier.startsWith("node:")) {
        addPackage(imported.specifier, fileName, imported.kind);
      }
    }
  }

  for (const record of records) {
    const hash = crypto.createHash("sha256");
    hash.update(fs.readFileSync(record.packagePath));
    for (const fileName of [...record.sourceSet].sort()) {
      hash.update("\0" + stablePath(fileName) + "\0");
      try {
        hash.update(fs.readFileSync(fileName));
      } catch {}
    }
    record.integrity = `sha256-${hash.digest("base64")}`;
    record.sources = [...record.sourceSet].sort();
    record.cacheKey = JSON.stringify({
      schema: analysisSchemaVersion,
      package: record.name,
      version: record.version,
      integrity: record.integrity,
      export: record.exportPath,
      conditions: record.conditions,
      typescript: ts.version,
      target: parsed.options.target ?? null,
      module: parsed.options.module ?? null,
    });
    record.identity = {
      name: record.name,
      version: record.version,
      export: record.exportPath,
      integrity: record.integrity,
      conditions: record.conditions,
      ...(record.declarationFile && { declaration: stablePath(record.declarationFile) }),
      ...(record.implementationFile && { implementation: stablePath(record.implementationFile) }),
    };
  }

  if (implementationFiles.size === 0) return { program, packages: records };
  const operationalProgram = ts.createProgram({
    rootNames: [...new Set([...parsed.fileNames, ...implementationFiles])],
    options: {
      ...parsed.options,
      allowJs: true,
      checkJs: false,
      noEmit: true,
      maxNodeModuleJsDepth: Math.max(parsed.options.maxNodeModuleJsDepth ?? 0, 100),
    },
    projectReferences: parsed.projectReferences,
  });
  return { program: operationalProgram, packages: records };
}
