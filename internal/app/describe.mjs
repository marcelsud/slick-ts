function analyzeDescriptions(program, graph, projectRoot, ts, packages) {
  const checker = program.getTypeChecker();
  const descriptions = [];
  const byCanonical = new Map();

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

  function isCallable(node) {
    return ts.isFunctionDeclaration(node) || ts.isMethodDeclaration(node) ||
      ts.isGetAccessorDeclaration(node) || ts.isSetAccessorDeclaration(node) ||
      ts.isConstructorDeclaration(node) || ts.isFunctionExpression(node) || ts.isArrowFunction(node);
  }

  function nameNode(node) {
    if (node.name) return node.name;
    if (ts.isConstructorDeclaration(node)) return node.parent.name;
    const parent = node.parent;
    if ((ts.isVariableDeclaration(parent) || ts.isPropertyDeclaration(parent) || ts.isPropertyAssignment(parent)) && parent.name) {
      return parent.name;
    }
    return node;
  }

  function resolveAlias(symbol) {
    return symbol && symbol.flags & ts.SymbolFlags.Alias ? checker.getAliasedSymbol(symbol) : symbol;
  }

  function primitiveName(type) {
    const flags = type.flags;
    if (flags & ts.TypeFlags.Any) return "any";
    if (flags & ts.TypeFlags.Unknown) return "unknown";
    if (flags & ts.TypeFlags.Never) return "never";
    if (flags & ts.TypeFlags.Void) return "void";
    if (flags & ts.TypeFlags.Undefined) return "undefined";
    if (flags & ts.TypeFlags.Null) return "null";
    if (flags & ts.TypeFlags.BooleanLike) return "boolean";
    if (flags & ts.TypeFlags.StringLike) return "string";
    if (flags & ts.TypeFlags.NumberLike) return "number";
    if (flags & ts.TypeFlags.BigIntLike) return "bigint";
    if (flags & ts.TypeFlags.ESSymbolLike) return "symbol";
    return undefined;
  }

  function sortTypes(values) {
    return values.sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
  }
  function typeName(type, symbol) {
    if (!symbol) return type.intrinsicName ?? (type.flags & ts.TypeFlags.Object ? "object" : `type:${type.flags}`);
    try {
      return checker.getFullyQualifiedName(symbol).replace(/^".*"\./, "");
    } catch {
      return symbol.getName();
    }
  }


  function typeDescription(type, location, depth = 0, seen = new Set()) {
    const primitive = primitiveName(type);
    if (type.isLiteral?.()) {
      return { kind: "literal", name: primitive, value: String(type.value ?? (type.intrinsicName ?? "")) };
    }
    if (primitive) return { kind: "primitive", name: primitive };
    const symbol = type.aliasSymbol ?? type.symbol;
    const name = typeName(type, symbol);
    if (depth >= 4 || seen.has(type)) return { kind: "reference", name };
    const nextSeen = new Set(seen);
    nextSeen.add(type);
    if (type.isUnion?.()) return { kind: "union", members: sortTypes(type.types.map((value) => typeDescription(value, location, depth + 1, nextSeen))) };
    if (type.isIntersection?.()) return { kind: "intersection", members: sortTypes(type.types.map((value) => typeDescription(value, location, depth + 1, nextSeen))) };
    if (checker.isArrayType(type)) {
      const element = checker.getTypeArguments(type)[0] ?? checker.getAnyType();
      return { kind: "array", element: typeDescription(element, location, depth + 1, nextSeen) };
    }
    if (checker.isTupleType(type)) {
      return { kind: "tuple", elements: checker.getTypeArguments(type).map((value) => typeDescription(value, location, depth + 1, nextSeen)) };
    }
    if (type.flags & ts.TypeFlags.TypeParameter) return { kind: "type_parameter", name };
    const signatures = checker.getSignaturesOfType(type, ts.SignatureKind.Call);
    if (signatures.length > 0) {
      const signature = signatures[0];
      return {
        kind: "callable",
        parameters: parameterDescriptions(signature, location, depth + 1, nextSeen),
        return: typeDescription(checker.getReturnTypeOfSignature(signature), location, depth + 1, nextSeen),
      };
    }
    const typeArguments = type.objectFlags & ts.ObjectFlags.Reference ? checker.getTypeArguments(type) : [];
    const expandProperties = !symbol || symbol.name === "__type" ||
      (symbol.declarations ?? []).some((declaration) => {
        const fileName = path.resolve(declaration.getSourceFile().fileName);
        const relative = path.relative(projectRoot, fileName);
        const authored = !declaration.getSourceFile().isDeclarationFile && relative !== "" && relative !== ".." && !relative.startsWith(`..${path.sep}`);
        return authored && (ts.isInterfaceDeclaration(declaration) ||
          ts.isClassDeclaration(declaration) && !declaration.members.some((member) =>
            member.name && ts.isPrivateIdentifier(member.name) ||
            member.modifiers?.some((modifier) => modifier.kind === ts.SyntaxKind.PrivateKeyword || modifier.kind === ts.SyntaxKind.ProtectedKeyword)));
      });
    const properties = depth < 2 && expandProperties ? checker.getPropertiesOfType(type).map((property) => {
      const declaration = property.valueDeclaration ?? property.declarations?.[0] ?? location;
      return {
        name: property.name,
        optional: Boolean(property.flags & ts.SymbolFlags.Optional),
        readonly: (property.declarations ?? []).some((value) =>
          value.modifiers?.some((modifier) => modifier.kind === ts.SyntaxKind.ReadonlyKeyword),
        ),
        type: typeDescription(checker.getTypeOfSymbolAtLocation(property, declaration), declaration, depth + 1, nextSeen),
      };
    }).sort((left, right) => left.name.localeCompare(right.name)) : [];
    return {
      kind: properties.length > 0 ? "object" : "reference",
      name,
      ...(typeArguments.length > 0 && { arguments: typeArguments.map((value) => typeDescription(value, location, depth + 1, nextSeen)) }),
      ...(properties.length > 0 && { properties }),
    };
  }

  function parameterDescriptions(signature, location, depth = 0, seen = new Set()) {
    return signature.parameters.map((parameter) => {
      const declaration = parameter.valueDeclaration ?? parameter.declarations?.[0] ?? location;
      return {
        name: parameter.name,
        optional: Boolean(parameter.flags & ts.SymbolFlags.Optional) || Boolean(declaration.questionToken || declaration.initializer),
        rest: Boolean(declaration.dotDotDotToken),
        type: typeDescription(checker.getTypeOfSymbolAtLocation(parameter, declaration), declaration, depth, seen),
      };
    });
  }

  function typeParameters(signature, location) {
    return (signature.typeParameters ?? []).map((parameter) => ({
      name: parameter.symbol?.name ?? "T",
      ...(parameter.getConstraint() && { constraint: typeDescription(parameter.getConstraint(), location) }),
      ...(parameter.getDefault() && { default: typeDescription(parameter.getDefault(), location) }),
    }));
  }

  function declarationTypeParameters(node) {
    return (node.typeParameters ?? []).map((parameter) => ({
      name: parameter.name.text,
      ...(parameter.constraint && { constraint: typeDescription(checker.getTypeFromTypeNode(parameter.constraint), parameter) }),
      ...(parameter.default && { default: typeDescription(checker.getTypeFromTypeNode(parameter.default), parameter) }),
    }));
  }

  function modifierKinds(node) {
    const result = new Set(node.modifiers?.map((modifier) => modifier.kind) ?? []);
    let current = node.parent;
    while (current && !ts.isSourceFile(current)) {
      for (const modifier of current.modifiers ?? []) result.add(modifier.kind);
      if (ts.isVariableStatement(current) || ts.isClassDeclaration(current) || ts.isModuleDeclaration(current)) break;
      current = current.parent;
    }
    return result;
  }

  function visibility(node, symbol) {
    const kinds = modifierKinds(node);
    if (node.name && ts.isPrivateIdentifier(node.name) || kinds.has(ts.SyntaxKind.PrivateKeyword)) return "private";
    if (kinds.has(ts.SyntaxKind.ProtectedKeyword)) return "protected";
    if (
      kinds.has(ts.SyntaxKind.PublicKeyword) ||
      (node.parent && (ts.isClassDeclaration(node.parent) || ts.isClassExpression(node.parent)))
    ) return "public";
    const moduleSymbol = node.getSourceFile().symbol;
    const exported = moduleSymbol && checker.getExportsOfModule(moduleSymbol)
      .some((value) => resolveAlias(value) === symbol);
    if (kinds.has(ts.SyntaxKind.ExportKeyword) || exported) return "exported";
    return "local";
  }

  function signatureDescriptions(symbol, node) {
    let signatures = [];
    if (symbol) {
      signatures = checker.getSignaturesOfType(
        checker.getTypeOfSymbolAtLocation(symbol, node),
        ts.isConstructorDeclaration(node) ? ts.SignatureKind.Construct : ts.SignatureKind.Call,
      );
    }
    if (signatures.length === 0 && isCallable(node)) {
      const direct = checker.getSignatureFromDeclaration(node);
      if (direct) signatures = [direct];
    }
    return signatures.map((signature) => ({
      typeParameters: typeParameters(signature, node),
      parameters: parameterDescriptions(signature, node),
      return: typeDescription(checker.getReturnTypeOfSignature(signature), node),
    }));
  }

  function kind(node) {
    if (ts.isFunctionDeclaration(node) || ts.isFunctionExpression(node) || ts.isArrowFunction(node)) return "function";
    if (ts.isMethodDeclaration(node)) return "method";
    if (ts.isGetAccessorDeclaration(node)) return "getter";
    if (ts.isSetAccessorDeclaration(node)) return "setter";
    if (ts.isConstructorDeclaration(node)) return "constructor";
    if (ts.isClassDeclaration(node) || ts.isClassExpression(node)) return "class";
    if (ts.isModuleDeclaration(node)) return "namespace";
    return "symbol";
  }

  function documentation(symbol) {
    return symbol ? ts.displayPartsToString(symbol.getDocumentationComment(checker)) : "";
  }
  function exportedNamesFor(symbol, packageIdentity) {
    let files;
    if (packageIdentity) {
      const dependency = packages.find((value) =>
        value.identity.name === packageIdentity.name && value.identity.export === packageIdentity.export,
      );
      files = dependency?.declarationFile ? [sourceFile(dependency.declarationFile)] : [];
    } else {
      files = program.getSourceFiles().filter((file) => {
        const relative = path.relative(projectRoot, path.resolve(file.fileName));
        return !file.isDeclarationFile && relative !== "" && relative !== ".." &&
          !relative.startsWith(`..${path.sep}`) && !relative.split(path.sep).includes("node_modules");
      });
    }
    const names = new Set();
    for (const file of files ?? []) {
      if (!file?.symbol) continue;
      for (const exported of checker.getExportsOfModule(file.symbol)) {
        if (resolveAlias(exported) === symbol) names.add(exported.name);
      }
    }
    return [...names].sort();
  }


  function aliases(canonical, name, packageIdentity, symbol, node) {
    const qualified = canonical.includes("::") ? canonical.split("::")[1] : canonical;
    const values = new Set([canonical, qualified, name]);
    const publicNames = !qualified.includes(".") ? exportedNamesFor(symbol, packageIdentity) : [];
    for (const publicName of publicNames) values.add(publicName);
    let container = node.parent;
    while (container && !ts.isSourceFile(container) &&
      !ts.isClassDeclaration(container) && !ts.isModuleDeclaration(container)) {
      container = container.parent;
    }
    if (qualified.includes(".") && container?.name) {
      const containerSymbol = resolveAlias(checker.getSymbolAtLocation(container.name));
      const suffix = qualified.slice(qualified.indexOf(".") + 1);
      for (const publicName of exportedNamesFor(containerSymbol, packageIdentity)) {
        values.add(`${publicName}.${suffix}`);
        if (packageIdentity) {
          const subpath = packageIdentity.export === "." ? "" : packageIdentity.export.slice(1);
          values.add(`${packageIdentity.name}${subpath}.${publicName}.${suffix}`);
          values.add(`${packageIdentity.name}${subpath}#${publicName}.${suffix}`);
        }
      }
    }
    if (packageIdentity) {
      const subpath = packageIdentity.export === "." ? "" : packageIdentity.export.slice(1);
      values.add(`${packageIdentity.name}${subpath}.${qualified}`);
      values.add(`${packageIdentity.name}${subpath}#${qualified}`);
      for (const publicName of publicNames) {
        values.add(`${packageIdentity.name}${subpath}.${publicName}`);
        values.add(`${packageIdentity.name}${subpath}#${publicName}`);
      }
    }
    return [...values].filter(Boolean).sort();
  }

  function declaredMembers(node) {
    const children = ts.isModuleDeclaration(node) && node.body && ts.isModuleBlock(node.body)
      ? node.body.statements
      : node.members ?? [];
    return children
      .filter((child) => child.name)
      .map((child) => child.name.getText(child.getSourceFile()));
  }

  function makeDescription(node, canonical, packageIdentity, members = []) {
    const namedNode = nameNode(node);
    const symbol = resolveAlias(checker.getSymbolAtLocation(namedNode));
    const signatures = signatureDescriptions(symbol, node);
    const primary = signatures[0];
    const name = canonical.split(".").pop().split("::").pop().replace(/^(get|set):/, "");
    return {
      canonicalName: canonical,
      name,
      kind: kind(node),
      visibility: visibility(node, symbol),
      documentation: documentation(symbol),
      aliases: aliases(canonical, name, packageIdentity, symbol, node),
      location: { path: stablePath(node.getSourceFile().fileName), range: sourceRange(namedNode) },
      typeParameters: primary?.typeParameters ?? declarationTypeParameters(node),
      parameters: primary?.parameters ?? [],
      ...(primary && { return: primary.return }),
      signatures,
      members: [...new Set([...members, ...declaredMembers(node)])].sort(),
      ...(packageIdentity && { package: packageIdentity }),
    };
  }

  function sourceFile(fileName) {
    return program.getSourceFile(fileName) ?? program.getSourceFiles().find((value) => path.resolve(value.fileName) === path.resolve(fileName));
  }

  function exportsOf(fileName) {
    const symbol = sourceFile(fileName)?.symbol;
    return symbol ? new Map(checker.getExportsOfModule(symbol).map((value) => [value.name, resolveAlias(value)])) : new Map();
  }

  function declaredNode(dependency, canonical) {
    const parts = canonical.split("::")[1]?.split(".") ?? [];
    if (parts.length === 0) return undefined;
    const declarationExports = exportsOf(dependency.declarationFile);
    let symbol = declarationExports.get(parts[0].replace(/^(get|set):/, ""));
    if (!symbol && dependency.implementationFile) {
      const implementationName = parts[0].replace(/^(get|set):/, "");
      const implementationExport = [...exportsOf(dependency.implementationFile)]
        .find(([, value]) => value.name === implementationName);
      if (implementationExport) symbol = declarationExports.get(implementationExport[0]);
    }
    for (const part of parts.slice(1)) {
      const name = part.replace(/^(get|set):/, "");
      symbol = symbol?.members?.get(name) ?? symbol?.exports?.get(name);
      symbol = resolveAlias(symbol);
    }
    if (!symbol) return undefined;
    const declarations = symbol.declarations ?? [];
    const prefix = parts.at(-1)?.split(":")[0];
    return declarations.find((value) => prefix === "get" ? ts.isGetAccessorDeclaration(value) : prefix === "set" ? ts.isSetAccessorDeclaration(value) : isCallable(value)) ?? declarations[0];
  }

  function authoredNode(entry) {
    const file = sourceFile(path.resolve(projectRoot, entry.location.path));
    let found;
    function visit(node) {
      if (found) return;
      if (isCallable(node) && sourceRange(nameNode(node)).start.offset === entry.location.range.start.offset) {
        found = node;
        return;
      }
      ts.forEachChild(node, visit);
    }
    if (file) visit(file);
    return found;
  }

  for (const entry of graph) {
    const dependency = entry.package && packages.find((value) => value.identity.name === entry.package.name && value.identity.export === entry.package.export);
    const node = dependency?.declarationFile ? declaredNode(dependency, entry.symbol) : authoredNode(entry);
    if (!node) continue;
    const description = makeDescription(node, entry.symbol, entry.package);
    descriptions.push(description);
    byCanonical.set(description.canonicalName, description);
  }

  function containerCanonical(node) {
    const parts = [node.name?.getText(node.getSourceFile())];
    let current = node.parent;
    while (current && !ts.isSourceFile(current)) {
      if ((ts.isClassDeclaration(current) || ts.isModuleDeclaration(current)) && current.name) {
        parts.unshift(current.name.getText(current.getSourceFile()));
      }
      current = current.parent;
    }
    return `${stablePath(node.getSourceFile().fileName)}::${parts.filter(Boolean).join(".")}`;
  }

  const packageBySource = new Map();
  for (const dependency of packages) {
    for (const fileName of dependency.sources ?? []) packageBySource.set(path.resolve(fileName), dependency);
  }
  for (const file of program.getSourceFiles()) {
    const absolute = path.resolve(file.fileName);
    const relative = path.relative(projectRoot, absolute);
    const authored = !file.isDeclarationFile && relative !== "" && relative !== ".." &&
      !relative.startsWith(`..${path.sep}`) && !relative.split(path.sep).includes("node_modules");
    const dependency = packageBySource.get(absolute);
    if (!authored && !dependency) continue;
    function visit(node) {
      if ((ts.isClassDeclaration(node) || ts.isModuleDeclaration(node)) && node.name) {
        const canonical = containerCanonical(node);
        const directMembers = declaredMembers(node);
        const existing = byCanonical.get(canonical);
        if (existing) {
          existing.members = [...new Set([...existing.members, ...directMembers])].sort();
        } else {
          const declarationNode = dependency?.declarationFile ? declaredNode(dependency, canonical) : node;
          const description = makeDescription(declarationNode ?? node, canonical, dependency?.identity, directMembers);
          descriptions.push(description);
          byCanonical.set(canonical, description);
        }
      }
      ts.forEachChild(node, visit);
    }
    visit(file);
  }

  return descriptions.sort((left, right) => left.canonicalName.localeCompare(right.canonicalName));
}
