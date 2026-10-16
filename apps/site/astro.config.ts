import sitemap from "@astrojs/sitemap";
import { defineConfig } from "astro/config";
import { sitemapBase } from "./src/sitemap.ts";

// Vercel sets the production domain on its builds. Canonical and og:image
// links, the sitemap and robots.txt need an absolute URL, and local builds have
// no domain to use.
const domain = process.env["VERCEL_PROJECT_PRODUCTION_URL"];
if (!domain && process.env["VERCEL_ENV"] === "production") {
  throw new Error(
    "VERCEL_PROJECT_PRODUCTION_URL is not set, so the production build would point to localhost.",
  );
}

export default defineConfig({
  site: domain ? `https://${domain}` : "http://localhost:4321",
  // Compression drops the line break that Prettier puts between a word and an
  // inline tag, which glues them together.
  compressHTML: false,
  integrations: [sitemap({ filenameBase: sitemapBase })],
});
