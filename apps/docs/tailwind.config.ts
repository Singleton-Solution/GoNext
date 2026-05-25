/**
 * Tailwind v3 configuration for @gonext/docs.
 *
 * Mirrors the admin app's Tailwind extension so the same Living-Systems
 * tokens (cream paper, forest ink, emerald + lavender) resolve to the
 * same Tailwind utilities. The single source of truth for values is
 * `docs/design/colors_and_type.css`; the mirror lives at
 * `src/styles/tokens.css`. When those change, this file must change too.
 *
 * Font families resolve to the CSS custom properties that
 * `next/font/google` injects on the <html> element from
 * `app/layout.tsx`. The fallback chain inside each entry is what
 * appears before the self-hosted face is ready, and in JSDOM tests
 * where next/font is not running.
 */
import type { Config } from 'tailwindcss';

const config: Config = {
  content: [
    './app/**/*.{ts,tsx,mdx}',
    './components/**/*.{ts,tsx}',
    './lib/**/*.{ts,tsx}',
    './src/**/*.{ts,tsx}',
  ],
  theme: {
    extend: {
      colors: {
        paper: {
          DEFAULT: '#F5F2EA',
          '2': '#EFEBE0',
          '3': '#E6E1D2',
          '4': '#DAD3BD',
        },
        forest: {
          DEFAULT: '#0E1A14',
          '2': '#18261E',
          '3': '#22322A',
          border: '#2C3D33',
        },
        ink: {
          DEFAULT: '#0E1A14',
          soft: '#1F2D26',
        },
        fg: {
          muted: '#4A5C52',
          subtle: '#6B7B72',
          faint: '#94A199',
          'on-forest': '#F0EAD8',
          'on-forest-muted': '#A8B5AC',
        },
        border: {
          DEFAULT: '#D9D2C0',
          strong: '#B8B09A',
          subtle: '#E8E2D1',
        },
        emerald: {
          DEFAULT: '#10B981',
          bright: '#34D399',
          deep: '#047857',
          soft: '#D1FAE5',
          ink: '#022C22',
        },
        lavender: {
          DEFAULT: '#A78BFA',
          deep: '#7C3AED',
          soft: '#EDE9FE',
        },
        success: {
          DEFAULT: '#059669',
          soft: '#D1FAE5',
        },
        warning: {
          DEFAULT: '#D97706',
          soft: '#FEF3C7',
        },
        danger: {
          DEFAULT: '#DC2626',
          soft: '#FEE2E2',
        },
      },
      fontFamily: {
        display: ['var(--font-display)', 'Archivo', 'system-ui', 'sans-serif'],
        sans: ['var(--font-sans)', 'Geist', 'system-ui', 'sans-serif'],
        serif: ['var(--font-serif)', 'Instrument Serif', 'Georgia', 'serif'],
        mono: ['var(--font-mono)', 'Geist Mono', 'ui-monospace', 'monospace'],
      },
      fontSize: {
        '2xs': ['11px', { lineHeight: '1.4' }],
        xs: ['12px', { lineHeight: '1.4' }],
        sm: ['13px', { lineHeight: '1.5' }],
        base: ['14px', { lineHeight: '1.5' }],
        md: ['15px', { lineHeight: '1.5' }],
        lg: ['17px', { lineHeight: '1.4' }],
        xl: ['20px', { lineHeight: '1.4' }],
        '2xl': ['24px', { lineHeight: '1.2' }],
        '3xl': ['32px', { lineHeight: '1.15' }],
        '4xl': ['44px', { lineHeight: '1.05' }],
        '5xl': ['64px', { lineHeight: '1.0' }],
        '6xl': ['96px', { lineHeight: '1.0' }],
      },
      letterSpacing: {
        tight: '-0.03em',
        normal: '-0.005em',
        wide: '0.04em',
      },
      spacing: {
        '1': '4px',
        '2': '8px',
        '3': '12px',
        '4': '16px',
        '5': '20px',
        '6': '24px',
        '7': '32px',
        '8': '48px',
        '9': '64px',
        '10': '96px',
      },
      borderRadius: {
        xs: '4px',
        sm: '6px',
        md: '8px',
        lg: '12px',
        xl: '16px',
        pill: '999px',
      },
      boxShadow: {
        xs: '0 1px 2px rgba(14, 26, 20, 0.04)',
        sm: '0 1px 3px rgba(14, 26, 20, 0.06), 0 1px 2px rgba(14, 26, 20, 0.04)',
        md: '0 6px 14px -4px rgba(14, 26, 20, 0.08), 0 2px 6px -2px rgba(14, 26, 20, 0.04)',
        lg: '0 16px 32px -10px rgba(14, 26, 20, 0.14), 0 4px 10px -4px rgba(14, 26, 20, 0.06)',
        focus: '0 0 0 3px rgba(16, 185, 129, 0.22)',
      },
      transitionTimingFunction: {
        brand: 'cubic-bezier(0.2, 0.7, 0.2, 1)',
      },
      transitionDuration: {
        fast: '100ms',
        DEFAULT: '160ms',
        slow: '260ms',
      },
    },
  },
  plugins: [],
};

export default config;
