import type { APIRoute } from "astro";
import { sitemapBase } from "../sitemap.ts";

export const GET: APIRoute = ({ site }) =>
  new Response(
    `User-agent: *\nAllow: /\n\nSitemap: ${new URL(`${sitemapBase}-index.xml`, site).href}\n`,
  );
