'use client';

/**
 * PrivacyForm — client wrapper that wires the generic SettingsForm to
 * the registry's PATCH endpoint. The schema lives in ./schema.ts.
 */
import type { ReactElement } from 'react';
import { SettingsForm } from '../SettingsForm';
import { patchSettings } from '../api';
import type { SettingsSection, SettingsValues } from '../types';
import { PRIVACY_SCHEMA } from './schema';

export interface PrivacyFormProps {
  initialValues: SettingsValues;
  banner?: string;
  sections?: readonly SettingsSection[];
}

export function PrivacyForm({
  initialValues,
  banner,
  sections,
}: PrivacyFormProps): ReactElement {
  return (
    <SettingsForm
      schema={PRIVACY_SCHEMA}
      initialValues={initialValues}
      onSubmit={patchSettings}
      banner={banner}
      sections={sections}
    />
  );
}
