import { defineConfig, globalIgnores } from "eslint/config";
import tseslint from "typescript-eslint";

export default defineConfig([
  globalIgnores(["dependencies/**", ".build/**", "dist/**", "node_modules/**"]),
  ...tseslint.configs.strictTypeChecked.map((configuration) => ({
    ...configuration,
    files: ["**/*.ts"],
  })),
  {
    files: ["**/*.ts"],
    languageOptions: {
      parserOptions: {
        project: ["./tsconfig.json", "./tsconfig.tests.json"],
        tsconfigRootDir: import.meta.dirname,
      },
    },
    rules: {
      "@typescript-eslint/no-non-null-assertion": "error",
      "@typescript-eslint/switch-exhaustiveness-check": "error",
      "@typescript-eslint/restrict-template-expressions": [
        "error",
        { allowNumber: true, allowBoolean: false },
      ],
    },
  },
  {
    files: ["web/state.ts", "web/protocol.ts", "web/i18n.ts"],
    rules: {
      "no-restricted-globals": [
        "error",
        "window",
        "document",
        "navigator",
        "localStorage",
        "fetch",
      ],
      "no-restricted-imports": [
        "error",
        { patterns: ["./app", "./bridge", "node:*", "bun:*"] },
      ],
    },
  },
]);
