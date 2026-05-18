// Flat config shim so `eslint <file>` works under ESLint v9+ when invoked
// outside of Raycast's own toolchain (e.g. editor/hook lint runs).
// Real linting is still done via `bun run lint` (ray lint), which uses the
// legacy .eslintrc.json + bundled ESLint v8.
const tsParser = require("@typescript-eslint/parser");

module.exports = [
  {
    files: ["**/*.{ts,tsx}"],
    languageOptions: {
      parser: tsParser,
      ecmaVersion: "latest",
      sourceType: "module",
      parserOptions: {
        ecmaFeatures: { jsx: true },
      },
    },
    rules: {},
  },
  {
    files: ["**/*.{js,jsx}"],
    languageOptions: {
      ecmaVersion: "latest",
      sourceType: "module",
    },
    rules: {},
  },
];
