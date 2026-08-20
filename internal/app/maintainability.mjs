function analyzeMaintainability(ts, collected) {
  if (process.env.SLICK_MAINTAINABILITY !== "1") return undefined;
  const printer = ts.createPrinter({ removeComments: true, newLine: ts.NewLineKind.LineFeed });

  function metrics(node) {
    const sourceFile = node.getSourceFile();
    const text = node.body?.getText(sourceFile) ?? node.getText(sourceFile);
    const scanner = ts.createScanner(ts.ScriptTarget.Latest, true, ts.LanguageVariant.Standard, text);
    const operators = new Map();
    const operands = new Map();
    let operatorCount = 0;
    let operandCount = 0;
    for (let token = scanner.scan(); token !== ts.SyntaxKind.EndOfFileToken; token = scanner.scan()) {
      const value = scanner.getTokenText();
      if (token === ts.SyntaxKind.Identifier || token === ts.SyntaxKind.PrivateIdentifier ||
          token === ts.SyntaxKind.StringLiteral || token === ts.SyntaxKind.NumericLiteral ||
          token === ts.SyntaxKind.BigIntLiteral || token === ts.SyntaxKind.NoSubstitutionTemplateLiteral ||
          token === ts.SyntaxKind.TrueKeyword || token === ts.SyntaxKind.FalseKeyword ||
          token === ts.SyntaxKind.NullKeyword || token === ts.SyntaxKind.ThisKeyword) {
        operandCount++;
        operands.set(value, (operands.get(value) ?? 0) + 1);
      } else if (![ts.SyntaxKind.OpenBraceToken, ts.SyntaxKind.CloseBraceToken,
          ts.SyntaxKind.OpenParenToken, ts.SyntaxKind.CloseParenToken,
          ts.SyntaxKind.OpenBracketToken, ts.SyntaxKind.CloseBracketToken,
          ts.SyntaxKind.CommaToken, ts.SyntaxKind.SemicolonToken].includes(token)) {
        operatorCount++;
        operators.set(`${token}:${value}`, (operators.get(`${token}:${value}`) ?? 0) + 1);
      }
    }
    const vocabulary = operators.size + operands.size;
    const length = operatorCount + operandCount;
    const volume = vocabulary === 0 || length === 0 ? 0 : length * Math.log2(vocabulary);
    const printed = printer.printNode(ts.EmitHint.Unspecified, node.body ?? node, sourceFile);
    const loc = Math.max(1, printed.split("\n").filter((line) => line.trim() !== "").length);
    return { distinctOperators: operators.size, distinctOperands: operands.size, operatorCount, operandCount, vocabulary, length, volume, loc };
  }

  const results = [];
  for (const functions of collected.functionsByFile.values()) {
    for (const callable of functions) {
      const halstead = metrics(callable.node);
      const safeVolume = Math.max(1, halstead.volume);
      const raw = (171 - 5.2 * Math.log(safeVolume) - 0.23 * callable.complexity - 16.2 * Math.log(halstead.loc)) * 100 / 171;
      results.push({
        symbol: callable.symbol,
        path: callable.path,
        range: callable.range,
        complexity: callable.complexity,
        ...halstead,
        index: Math.max(0, Math.min(100, raw)),
      });
    }
  }
  results.sort((left, right) => {
    const pathOrder = left.path < right.path ? -1 : left.path > right.path ? 1 : 0;
    return pathOrder || left.range.start.offset - right.range.start.offset;
  });
  return { results };
}
