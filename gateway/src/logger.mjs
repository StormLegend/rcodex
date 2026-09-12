function write(stream, level, args) {
  const line = `${new Date().toISOString()} ${level} ${args
    .map((value) => (typeof value === "string" ? value : JSON.stringify(value)))
    .join(" ")}`;
  stream.write(`${line}\n`);
}

export function createLogger({ verbose = false } = {}) {
  return {
    info: (...args) => write(process.stdout, "INFO", args),
    warn: (...args) => write(process.stderr, "WARN", args),
    error: (...args) => write(process.stderr, "ERROR", args),
    debug: (...args) => {
      if (verbose) write(process.stdout, "DEBUG", args);
    },
  };
}
