import { defineConfig, globalIgnores } from "eslint/config";
import astro from "eslint-plugin-astro";
import { createNodeResolver, importX } from "eslint-plugin-import-x";
import tseslint from "typescript-eslint";

export default defineConfig([
  globalIgnores(["dist/", ".astro/"]),
  { files: ["**/*.ts"], languageOptions: { parser: tseslint.parser } },
  astro.configs.base,
  {
    plugins: { "import-x": importX },
    // Symlinks stay as they are, so a workspace package resolves inside the
    // site's node_modules and passes, while a path into its folder fails.
    settings: {
      "import-x/resolver-next": [createNodeResolver({ symlinks: false })],
    },
    rules: {
      // The site reaches another project only through its package name, so
      // Moon and pnpm know about the dependency and the package exports
      // decide what it gets. A path that does not resolve would slip past
      // these checks, so it fails too.
      "import-x/no-relative-packages": "error",
      "import-x/no-restricted-paths": [
        "error",
        {
          basePath: import.meta.dirname,
          zones: [
            {
              target: ".",
              from: "../..",
              except: ["apps/site"],
              message: "Import another project through its package.",
            },
          ],
        },
      ],
      "import-x/no-unresolved": "error",
    },
  },
]);
