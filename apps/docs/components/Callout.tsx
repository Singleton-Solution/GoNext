/**
 * Callout component — boxes for "Note", "Warning", "Tip", etc.
 *
 * Used by first-party MDX pages. Filesystem-sourced markdown gets folded
 * into <blockquote> by the renderer; we may later promote `> [!NOTE]`
 * patterns into this component, but for now the two layers stay separate.
 *
 * Visual: emerald-soft tint for note/tip, warning-soft for warning,
 * danger-soft for danger. Each carries a circular ink-on-cream icon
 * (Archivo glyph) with an uppercase emerald-deep title. Mirrors the
 * .callout treatment in docs/design/ui_kits/docs/index.html.
 */
import type { ReactElement, ReactNode } from 'react';

type Variant = 'note' | 'tip' | 'warning' | 'danger';

interface CalloutProps {
  variant?: Variant;
  title?: string;
  children: ReactNode;
}

const ICONS: Record<Variant, string> = {
  note: 'i',
  tip: '*',
  warning: '!',
  danger: '!!',
};

export function Callout({ variant = 'note', title, children }: CalloutProps): ReactElement {
  return (
    <div className={`callout callout--${variant}`} role="note">
      <div className="callout__icon" aria-hidden="true">
        {ICONS[variant]}
      </div>
      <div className="callout__body">
        {title && <div className="callout__title">{title}</div>}
        <div className="callout__content">{children}</div>
      </div>
    </div>
  );
}
