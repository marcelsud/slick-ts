function analyzeArtifacts(ts, emitRoot, outputs) {
  if (!emitRoot || process.env.SLICK_ARTIFACTS !== "1") return undefined;
  const builtins = new Set(builtinModules.map((value) => value.replace(/^node:/, "")));

  function packageName(specifier) {
    if (specifier.startsWith("@")) return specifier.split("/").slice(0, 2).join("/");
    return specifier.split("/")[0];
  }

  function runtimeImports(fileName, source) {
    const sourceFile = ts.createSourceFile(fileName, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.JS);
    const imports = [];
    function add(node, specifier, kind) {
      if (specifier.startsWith(".") || specifier.startsWith("/")) return;
      const bare = specifier.replace(/^node:/, "");
      const builtin = builtins.has(bare);
      const start = sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile));
      imports.push({
        specifier,
        package: builtin ? "" : packageName(specifier),
        builtin,
        kind,
        line: start.line + 1,
        column: start.character + 1,
      });
    }
    function visit(node) {
      if (ts.isImportDeclaration(node) && ts.isStringLiteral(node.moduleSpecifier)) {
        add(node.moduleSpecifier, node.moduleSpecifier.text, "import");
      } else if (ts.isExportDeclaration(node) && node.moduleSpecifier && ts.isStringLiteral(node.moduleSpecifier)) {
        add(node.moduleSpecifier, node.moduleSpecifier.text, "export");
      } else if (ts.isImportEqualsDeclaration(node) && ts.isExternalModuleReference(node.moduleReference) && ts.isStringLiteral(node.moduleReference.expression)) {
        add(node.moduleReference.expression, node.moduleReference.expression.text, "require");
      } else if (ts.isCallExpression(node) && node.arguments.length === 1 && ts.isStringLiteral(node.arguments[0])) {
        if (ts.isIdentifier(node.expression) && node.expression.text === "require") add(node.arguments[0], node.arguments[0].text, "require");
        if (node.expression.kind === ts.SyntaxKind.ImportKeyword) add(node.arguments[0], node.arguments[0].text, "dynamic-import");
      }
      ts.forEachChild(node, visit);
    }
    visit(sourceFile);
    imports.sort((left, right) => left.line - right.line || left.column - right.column || (left.specifier < right.specifier ? -1 : left.specifier > right.specifier ? 1 : 0));
    return imports;
  }

  const files = [];
  for (const output of outputs) {
    const stagedPath = path.join(emitRoot, output.staged);
    const buffer = fs.readFileSync(stagedPath);
    const extension = path.extname(output.path).toLowerCase();
    const imports = [".js", ".mjs", ".cjs"].includes(extension)
      ? runtimeImports(output.path, buffer.toString("utf8"))
      : [];
    files.push({ path: output.path, staged: output.staged, bytes: buffer.byteLength, imports });
  }
  files.sort((left, right) => left.path < right.path ? -1 : left.path > right.path ? 1 : 0);
  return { totalBytes: files.reduce((sum, file) => sum + file.bytes, 0), files };
}
