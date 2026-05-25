'use client';

/**
 * Client wrapper that wires the generic `SettingsForm` to the PATCH endpoint
 * for General settings.
 *
 * Kept separate from the (server) page component so the heavy schema and the
 * client-only form code don't leak into the initial server payload.
 */
import type { ReactElement } from 'react';
import { SettingsForm } from '../SettingsForm';
import { patchSettings } from '../api';
import type { SettingsSection, SettingsValues } from '../types';
import { GENERAL_SCHEMA } from './schema';

export interface GeneralFormProps {
  initialValues: SettingsValues;
  banner?: string;
  sections?: readonly SettingsSection[];
}

export function GeneralForm({
  initialValues,
  banner,
  sections,
}: GeneralFormProps): ReactElement {
  return (
    <SettingsForm
      schema={GENERAL_SCHEMA}
      initialValues={initialValues}
      onSubmit={patchSettings}
      banner={banner}
      sections={sections}
    />
  );
}
