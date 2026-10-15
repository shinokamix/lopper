import { defineConfig } from "astro/config";

// Vercel sets the production domain on its builds. Canonical and og:image
// links need an absolute URL, and local builds have no domain to use.
const domain = process.env["VERCEL_PROJECT_PRODUCTION_URL"];

export default defineConfig({
  site: domain ? `https://${domain}` : "http://localhost:4321",
  // Compression drops the line break that Prettier puts between a word and an
  // inline tag, which glues them together.
  compressHTML: false,
});
