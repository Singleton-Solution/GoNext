/**
 * /users/invite — invite a new user to the workspace.
 *
 * Server shell wrapping the interactive `<InviteForm>` client island.
 * The shell stays trivial so the route compiles into a small initial
 * payload; JS only flips on for the form.
 */
import type { ReactElement } from 'react';
import { InviteForm } from './InviteForm';

export default function InviteUserPage(): ReactElement {
  return <InviteForm />;
}
