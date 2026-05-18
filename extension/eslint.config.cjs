// Flat config for vanilla MV3 service worker.
// Disables ESLint rules so background.js (chrome.* globals, console, etc.)
// doesn't report false positives when linted in isolation.
module.exports = [
  {
    files: ["**/*.js"],
    languageOptions: {
      ecmaVersion: "latest",
      sourceType: "module",
      globals: {
        chrome: "readonly",
        console: "readonly",
      },
    },
    rules: {},
  },
];
