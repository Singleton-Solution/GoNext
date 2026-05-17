'use client';

/**
 * Client-side error boundary for the Posts list.
 *
 * React Server Components can't be wrapped by `<ErrorBoundary>` directly
 * (componentDidCatch only runs on the client), so this is the canonical
 * pattern: a tiny class component on the client side that catches render
 * errors thrown by its children and shows a retry affordance.
 *
 * The server component (`page.tsx`) also catches its fetch failure and
 * renders a friendly empty/error state inline — this boundary is the
 * second line of defence for client-side render errors and for navigation
 * cases where the server component throws.
 */
import { Component, type ErrorInfo, type ReactNode } from 'react';
import styles from './posts.module.css';

interface PostsErrorBoundaryProps {
  children: ReactNode;
}

interface PostsErrorBoundaryState {
  error: Error | null;
}

export class PostsErrorBoundary extends Component<
  PostsErrorBoundaryProps,
  PostsErrorBoundaryState
> {
  public override state: PostsErrorBoundaryState = { error: null };

  public static getDerivedStateFromError(error: Error): PostsErrorBoundaryState {
    return { error };
  }

  public override componentDidCatch(error: Error, info: ErrorInfo): void {
    // Real telemetry plugs in here once doc 10 (observability) ships;
    // for the scaffold we surface the failure in the console so it's
    // discoverable in development.
    // eslint-disable-next-line no-console
    console.error('[posts] render error', error, info);
  }

  private readonly handleRetry = (): void => {
    this.setState({ error: null });
  };

  public override render(): ReactNode {
    if (this.state.error) {
      return (
        <div className={styles.error} role="alert">
          <h2>Couldn&apos;t load posts</h2>
          <p className="muted">
            Something went wrong while rendering the list. Try again, or
            reload the page if the problem persists.
          </p>
          <button
            type="button"
            className="btn-primary"
            onClick={this.handleRetry}
          >
            Retry
          </button>
        </div>
      );
    }
    return this.props.children;
  }
}
