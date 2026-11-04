import { readdirSync, readFileSync } from "node:fs";
import { join, relative, resolve } from "node:path";
import { compile } from "@mdx-js/mdx";
import { describe, expect, it } from "vitest";

// Every docs page must parse as MDX. A bare `<placeholder>` in prose is read
// as an unclosed JSX tag and fails the whole docs build, so this catches it
// before main goes red. Runs from the docs package dir (`npm test`), which is
// also the root the vitest include globs resolve against.
const contentDir = resolve(process.cwd(), "content");

function mdxFiles(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) return mdxFiles(path);
    return entry.name.endsWith(".mdx") ? [path] : [];
  });
}

describe("docs content MDX", () => {
  const files = mdxFiles(contentDir);

  it("finds the docs pages", () => {
    expect(files.length).toBeGreaterThan(0);
  });

  it.each(files.map((file) => [relative(contentDir, file), file]))(
    "%s compiles as MDX",
    async (_name, file) => {
      await expect(compile(readFileSync(file, "utf8"))).resolves.toBeDefined();
    },
  );
});
