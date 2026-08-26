import { useRouteError, isRouteErrorResponse, Link } from 'react-router-dom';

export default function RouteErrorBoundary() {
  const error = useRouteError();

  let title = 'Unexpected Error';
  let message = 'Something went wrong.';

  if (isRouteErrorResponse(error)) {
    title = `${error.status} ${error.statusText}`;
    message = error.data?.message || 'This page could not be loaded.';
  } else if (error instanceof Error) {
    message = error.message;
  }

  return (
    <div className="flex min-h-svh w-full flex-col items-center justify-center p-6 md:p-10 bg-background text-foreground">
      <div className="max-w-md text-center">
        <h1 className="text-2xl font-bold text-foreground mb-4">{title}</h1>
        <p className="text-muted-foreground mb-6">{message}</p>
        <Link
          to="/auth/login"
          className="inline-block px-4 py-2 bg-primary text-primary-foreground rounded hover:opacity-90"
        >
          Go to Login
        </Link>
      </div>
    </div>
  );
}
