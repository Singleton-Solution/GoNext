/**
 * /users/new — invite a new user to the workspace.
 *
 * Server shell wrapping the interactive `<InviteForm>` client island.
 * The shell stays trivial so the route compiles into a small initial
 * payload; JS only flips on for the form.
 *
 * Routing note: the list-page CTA links here (`/users/new`) and is
 * pinned by the UsersList test suite. The "new vs. invite" naming is
 * deliberately neutral — historically WordPress called this "new user",
 * and we keep the URL path stable for habit's sake.
 */
import type { ReactElement } from 'react';
import { InviteForm } from './InviteForm';

export default function NewUserPage(): ReactElement {
  return <InviteForm />;
}
