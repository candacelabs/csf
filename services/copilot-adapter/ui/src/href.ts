const SAFE_SCHEMES = ["http:", "https:", "mailto:"];

// A link is a link only if it goes somewhere a browser may safely follow;
// javascript:, data: and vbscript: hrefs render as their own text instead.
export function safeHref(href: string): string | null {
  try {
    const parsed = new URL(href, "http://adapter.invalid/");
    return SAFE_SCHEMES.includes(parsed.protocol) ? href : null;
  } catch {
    return null;
  }
}
