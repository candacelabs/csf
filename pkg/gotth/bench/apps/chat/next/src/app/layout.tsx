import type { Metadata } from 'next';
import Script from 'next/script';

import './chat.css';

/*
 * The root layout is a Server Component and stays one (§5.4: no 'use client' at
 * or near the root). Everything interactive lives two levels down, in
 * components/ChatLive.tsx.
 *
 * The shim is loaded with strategy="beforeInteractive", which puts a plain
 * <script src> in <head> ahead of Next's own bootstrap. §3.2 requires the
 * t_input listener to be registered "before any application script"; on this
 * stack the application script is React's hydration bundle, so this is the only
 * strategy that satisfies it. The gotth-live side achieves the same ordering
 * with a <script src> ahead of its runtime tag, and the file is byte-identical
 * on both (§2.0) — `npm run verify:shim` is what proves it.
 */

export const metadata: Metadata = {
  title: 'Next.js chat — gotth-live bench',
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <head>
        <Script src="/bench/shim.js" strategy="beforeInteractive" />
      </head>
      <body>{children}</body>
    </html>
  );
}
