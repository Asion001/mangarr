// Hook mistakes break pages in ways types can't catch. Missing effect
// dependencies are often deliberate here (see the comments at each), so
// exhaustive-deps stays off.
import tseslint from "typescript-eslint";
import reactHooks from "eslint-plugin-react-hooks";

export default [
  { ignores: ["dist/**", "src/api/schema.d.ts"] },
  {
    files: ["src/**/*.{ts,tsx}"],
    languageOptions: { parser: tseslint.parser },
    // the exhaustive-deps notes in the code say why a dependency is left out
    linterOptions: { reportUnusedDisableDirectives: "off" },
    plugins: { "react-hooks": reactHooks },
    rules: { "react-hooks/rules-of-hooks": "error" },
  },
];
