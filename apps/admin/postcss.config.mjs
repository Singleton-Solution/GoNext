/**
 * PostCSS config — required for Next.js to pick up Tailwind v3.
 *
 * Next.js auto-detects `postcss.config.{js,mjs,cjs}` at the app root
 * and runs the listed plugins over every imported `.css` file (we
 * import `./globals.css` from the root layout). Without this file
 * Next would fall through to its default postcss preset and silently
 * skip the Tailwind processing pass, so the `@tailwind base/components/utilities`
 * directives at the top of globals.css would never expand.
 */
export default {
  plugins: {
    tailwindcss: {},
    autoprefixer: {},
  },
};
