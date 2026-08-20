function emitBuild(program, ts, diagnostics, slickDiagnostics) {
  const emitRoot = process.env.SLICK_EMIT_ROOT;
  if (!emitRoot) return { diagnostics: [], outputs: [] };
  const hasErrors = [...diagnostics, ...slickDiagnostics]
    .some((diagnostic) => diagnostic.category === ts.DiagnosticCategory.Error || diagnostic.category === "error");
  if (hasErrors) return { diagnostics: [], outputs: [] };

  const outputs = [];
  try {
    const filesRoot = path.join(emitRoot, "files");
    fs.mkdirSync(filesRoot, { recursive: true });
    const result = program.emit(undefined, (fileName, data, writeByteOrderMark) => {
      const staged = `files/${outputs.length}`;
      fs.writeFileSync(
        path.join(emitRoot, staged),
        `${writeByteOrderMark ? "\uFEFF" : ""}${data}`,
      );
      outputs.push({ path: path.resolve(fileName), staged });
    });
    return {
      diagnostics: result.diagnostics,
      outputs: outputs.sort((left, right) => left.path.localeCompare(right.path)),
      ...(result.emitSkipped && result.diagnostics.length === 0 && {
        failure: { kind: "emit_failure", message: "TypeScript skipped JavaScript emit" },
      }),
    };
  } catch (error) {
    return {
      diagnostics: [],
      outputs: [],
      failure: { kind: "emit_failure", message: error instanceof Error ? error.message : String(error) },
    };
  }
}
