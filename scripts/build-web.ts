import { cp, mkdir } from "node:fs/promises";
import { resolve } from "node:path";

import { build } from "esbuild";

const root = resolve(import.meta.dirname, "..");
const output = resolve(root, ".build/web");
await mkdir(output, { recursive: true });
await build({
  absWorkingDir: root,
  entryPoints: { app: "web/app.ts", admin: "web/admin.ts" },
  outdir: output,
  bundle: true,
  format: "iife",
  target: "es2017",
  charset: "utf8",
  logLevel: "warning",
  legalComments: "inline",
});
for (const name of ["index.html", "app.css", "assets"]) {
  await cp(resolve(root, "web", name), resolve(output, name), {
    recursive: true,
  });
}

// Go embeds the LAN editor from its package directory.
const lan = resolve(root, "app/.build/lan");
await mkdir(lan, { recursive: true });
for (const name of ["admin.html", "admin.css"]) {
  await cp(resolve(root, "web", name), resolve(lan, name));
}
await cp(resolve(output, "admin.js"), resolve(lan, "admin.js"));
