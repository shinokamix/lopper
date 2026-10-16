import type { APIRoute } from "astro";
import { sitemapBase } from "../sitemap.ts";

// Each preview lives at its own vercel.app address and copies the production
// page, so crawlers stay off previews.
export const GET: APIRoute = ({ site }) =>
  new Response(
    process.env["VERCEL_ENV"] === "production"
      ? `User-agent: *\nAllow: /\n\nSitemap: ${new URL(`${sitemapBase}-index.xml`, site).href}\n`
      : "User-agent: *\nDisallow: /\n",
  );
