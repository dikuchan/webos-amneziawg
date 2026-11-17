import { cp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { resolve } from "node:path";

import appinfo from "../packaging/appinfo.json";

const root = resolve(import.meta.dirname, "..");
const version = (await readFile(resolve(root, "VERSION"), "utf8")).trim();
if (!/^\d+\.\d+\.\d+$/.test(version)) throw new Error("Invalid VERSION");
const kit = resolve(root, ".build/kit");
const app = resolve(root, ".build/app", appinfo.id);
const dist = resolve(root, "dist");
await rm(app, { recursive: true, force: true });
await rm(kit, { recursive: true, force: true });
await mkdir(resolve(kit, "scripts"), { recursive: true });
await mkdir(resolve(kit, "licenses"), { recursive: true });
await mkdir(app, { recursive: true });
await mkdir(dist, { recursive: true });
for (const name of ["awg", "amneziawg-go", "amneziawgctl"]) {
  const file = Bun.file(resolve(root, ".build/armv7/bin", name));
  if (!(await file.exists()) || file.size === 0)
    throw new Error(`Missing ARMv7 binary: ${name}`);
}
await cp(resolve(root, ".build/armv7/bin"), resolve(kit, "bin"), {
  recursive: true,
});
for (const name of ["boot.sh", "dns.sh"]) {
  await cp(resolve(root, "packaging", name), resolve(kit, "scripts", name));
}
for (const [source, name] of [
  ["LICENSE", "webos-amneziawg-MIT.txt"],
  ["dependencies/amneziawg-go/LICENSE", "amneziawg-go-MIT.txt"],
  ["dependencies/amneziawg-tools/COPYING", "amneziawg-tools-GPL-2.0.txt"],
  ["dependencies/musl/COPYRIGHT", "musl-COPYRIGHT.txt"],
  ["app/presets/LICENSE", "ru-direct-MIT.txt"],
  ["web/assets/AMNEZIA-LICENSE", "amnezia-client-GPL-3.0.txt"],
  ["web/assets/icons/LICENSE", "lucide-ISC.txt"],
  ["web/assets/fonts/Inter-OFL.txt", "Inter-OFL.txt"],
  ["web/assets/fonts/JetBrainsMono-OFL.txt", "JetBrainsMono-OFL.txt"],
] as const) {
  await cp(resolve(root, source), resolve(kit, "licenses", name));
}
await cp(resolve(root, "README.md"), resolve(kit, "README.md"));
for (const name of ["index.html", "app.js", "app.css", "assets"]) {
  await cp(resolve(root, ".build/web", name), resolve(app, name), {
    recursive: true,
  });
}
await mkdir(resolve(app, "payload"), { recursive: true });
for (const name of ["bin", "scripts"]) {
  await cp(resolve(kit, name), resolve(app, "payload", name), {
    recursive: true,
  });
}
await cp(resolve(kit, "licenses"), resolve(app, "licenses"), {
  recursive: true,
});
await writeFile(
  resolve(app, "appinfo.json"),
  JSON.stringify({ ...appinfo, version }, null, 2) + "\n",
);

async function run(command: string[]): Promise<void> {
  const child = Bun.spawn(command, {
    cwd: root,
    stdout: "inherit",
    stderr: "inherit",
  });
  if ((await child.exited) !== 0)
    throw new Error(`Packaging command failed: ${command[0] ?? "unknown"}`);
}
await run([
  resolve(root, "node_modules/.bin/ares-package"),
  "--outdir",
  dist,
  app,
]);
const ipk = `webos-amneziawg_${version}_all.ipk`;
await cp(resolve(dist, `${appinfo.id}_${version}_all.ipk`), resolve(dist, ipk));
await rm(resolve(dist, `${appinfo.id}_${version}_all.ipk`));
const archive = `webos-amneziawg-${version}-armv7.tar.gz`;
await run([
  "tar",
  "czf",
  resolve(dist, archive),
  "-C",
  kit,
  "bin",
  "scripts",
  "licenses",
  "README.md",
]);
const sums: string[] = [];
for (const name of [ipk, archive]) {
  const hasher = new Bun.CryptoHasher("sha256");
  hasher.update(await Bun.file(resolve(dist, name)).arrayBuffer());
  sums.push(`${hasher.digest("hex")}  ${name}\n`);
}
await writeFile(resolve(dist, "SHA256SUMS"), sums.join(""));
