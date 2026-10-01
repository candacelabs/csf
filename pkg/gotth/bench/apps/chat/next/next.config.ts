import { createRequire } from 'node:module';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import type { NextConfig } from 'next';

import { VARIANT } from './src/lib/variant';

/*
 * Chat app, Next.js side — build configuration (§5.1, §5.4).
 *
 * Everything here is either a production default the spec names or a fairness
 * constraint the spec imposes. Nothing is a tuning knob, because tuning one
 * side obliges equivalent tuning effort on the other and a disclosure (FR-73),
 * and there is nothing here worth that.
 */

const here = path.dirname(fileURLToPath(import.meta.url));

/*
 * §5.4 ships exactly ONE live-data implementation per build. Resolving the
 * transport at runtime would put all three in the client bundle and D1 would
 * charge Next.js for two transports the measured build never opens a socket
 * with. The alias is the seam; lib/transport/types.ts is the interface.
 */
const transportModule = path.join(here, 'src', 'lib', 'transport', `${VARIANT}.ts`);

const nextConfig: NextConfig = {
  /* §5.1: `next build` with output: 'standalone', self-hosted node server. */
  output: 'standalone',

  /*
   * The workspace root, so file tracing follows the hoisted node_modules at
   * bench/node_modules instead of stopping at this package and shipping a
   * standalone tree with no react in it.
   */
  outputFileTracingRoot: path.join(here, '..', '..', '..'),

  /*
   * Compression is the PROXY's job on both stacks, and it is off here so it
   * happens exactly once.
   *
   * §3.5 mandates gzip level 6 for the comparison figure on both sides and
   * calls serving one stack with brotli and the other with gzip a
   * disqualifying method error. The only way to guarantee one level for both
   * is to compress in the one container both stacks share — the §3.6 proxy,
   * whose Caddyfile sets `encode gzip 6`. Leaving Next's own gzip on as well
   * would either double-compress or make the effective level whichever layer
   * won, which is not a property either stack should have to prove.
   *
   * The measured container therefore serves identity-encoded bytes, exactly as
   * the gotth-live container does. Wire bytes on the plaintext proxy->app leg
   * (§4.6) are uncompressed on both sides; transfer bytes at the browser
   * (§3.5) are gzip-6 on both sides.
   */
  compress: false,

  /*
   * No source maps in production (§3.5's exclusion list says they are "not
   * served in production on either side" — this is what makes that true rather
   * than assumed).
   */
  productionBrowserSourceMaps: false,

  webpack(config) {
    config.resolve.alias = {
      ...config.resolve.alias,
      '@transport': transportModule,
    };
    return config;
  },
};

/*
 * §5.4's audit checklist requires committed @next/bundle-analyzer output. It is
 * loaded only when ANALYZE=true so the measured build does not carry the
 * plugin, and `npm run analyze` is the command bench/README.md documents.
 */
export default process.env.ANALYZE === 'true'
  ? createRequire(import.meta.url)('@next/bundle-analyzer')({
      enabled: true,
      openAnalyzer: false,
      /*
       * json, not the default static HTML. The HTML report is ~1 MB per app per
       * side and committing 6 MB of generated report to satisfy "output
       * committed" would be committing a screenshot of the evidence. The JSON
       * carries the same numbers, scripts/audit.mjs distils it into the small
       * table the checklist publishes, and the raw JSON is regenerable by the
       * documented command (FR-75).
       */
      analyzerMode: 'json',
    })(nextConfig)
  : nextConfig;
