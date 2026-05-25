---
name: gonext-design
description: Use this skill to generate well-branded interfaces and assets for GoNext (a CMS/hosting/commerce alternative to WordPress), either for production or throwaway prototypes/mocks. Contains the visual language, tokens, copy guidelines, logos, and UI kit recreations needed to design new screens in-brand.
user-invocable: true
---

# GoNext design skill

Read `README.md` in this skill folder first — it covers brand premise, voice, and visual foundations. Then explore:

- `colors_and_type.css` — CSS variables for every design token. Import this first in any new HTML artifact.
- `assets/` — logo mark, wordmark, and favicon SVGs. Copy them into the artifact rather than hot-linking.
- `fonts/README.md` — font sources (all Google Fonts).
- `preview/` — small reference cards for each atomic component (buttons, colors, type, etc).
- `ui_kits/` — six full UI surfaces you can fork: `marketing`, `admin`, `editor`, `marketplace`, `templates`, `docs`.

## How to design in-brand

1. **Start from a UI kit.** If you're mocking a new screen, find the closest kit in `ui_kits/` and copy its index.html as a starting point. Don't rebuild chrome from scratch.
2. **Use the tokens.** Never hardcode colors or font sizes — always use `var(--ink)`, `var(--volt)`, `var(--font-display)`, etc.
3. **Lead with type.** GoNext is type-led. Big Space Grotesk display headlines with a `.` ending. Monospace kicker labels above them.
4. **The volt is precious.** One accent per screen, used to draw the eye to the most important thing.
5. **Hard shadows, not blur.** Use `var(--sh-hard-sm/md)` for lift on hover. Never use blur shadows.
6. **Copy tone**: confident, direct, faintly snarky. Avoid marketing fluff. See README "Content Fundamentals" for examples.

If the user invokes this skill without other guidance, ask what they want to design (a new page, a deck, a feature mock?) and start by reading the relevant ui_kit.
