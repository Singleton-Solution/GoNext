/**
 * Layout for `/settings/*` routes.
 *
 * Exists only to scope the settings-specific CSS to this subtree without
 * touching the global stylesheet. Renders children pass-through.
 */
import type { ReactElement, ReactNode } from 'react';
import './settings.css';

export default function SettingsLayout({
  children,
}: {
  children: ReactNode;
}): ReactElement {
  return <>{children}</>;
}
