/**
 * PostCSS config — required for Next.js to pick up Tailwind v3.
 *
 * Mirrors `apps/admin/postcss.config.mjs`. Next.js auto-detects this
 * file at the app root and runs the listed plugins over every imported
 * `.css` file. We import the global stylesheet (which lives at
 * `styles/docs.css`, kept under that name for backwards-compatible
 * import paths) from the root layout; without the postcss config the
 * `@tailwind` directives at the top of that file would be left as
 * literal text.
 */
export default {
  plugins: {
    tailwindcss: {},
    autoprefixer: {},
  },
};
