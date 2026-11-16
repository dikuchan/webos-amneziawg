import { readFile } from "node:fs/promises";
import { resolve, sep } from "node:path";

import { build } from "esbuild";

const root = resolve(import.meta.dirname, "..");
const web = resolve(root, process.env["AWG_PREVIEW_DIR"] ?? ".build/web");
await build({
  entryPoints: [resolve(root, "tests/mock-bridge.ts")],
  outfile: resolve(root, ".build/mock.js"),
  bundle: true,
  format: "iife",
  target: "es2017",
});
const page = (await readFile(resolve(web, "index.html"), "utf8")).replace(
  /<script\b/,
  '<script src="/mock.js"></script><script',
);
const server = Bun.serve({
  hostname: "127.0.0.1",
  port: 8091,
  async fetch(request) {
    const url = new URL(request.url);
    if (url.pathname === "/")
      return new Response(page, {
        headers: { "Content-Type": "text/html; charset=utf-8" },
      });
    if (url.pathname === "/mock.js")
      return new Response(Bun.file(resolve(root, ".build/mock.js")));
    const path = resolve(web, "." + url.pathname);
    if (!path.startsWith(web + sep))
      return new Response("Not found", { status: 404 });
    const file = Bun.file(path);
    if (!(await file.exists()) || file.size > 2 * 1024 * 1024)
      return new Response("Not found", { status: 404 });
    return new Response(file);
  },
});
process.on("SIGTERM", () => {
  void server.stop(true);
});
