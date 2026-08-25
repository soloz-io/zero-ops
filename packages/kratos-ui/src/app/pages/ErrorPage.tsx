import { useSearchParams } from 'react-router-dom';

export default function ErrorPage() {
  const [searchParams] = useSearchParams();
  const id = searchParams.get('id');
  const message = searchParams.get('message');

  return (
    <div className="flex min-h-svh w-full flex-col items-center justify-center p-6 md:p-10 bg-background text-foreground">
      <div className="max-w-md text-center">
        <h1 className="text-2xl font-bold text-foreground mb-4">Error</h1>
        {id && (
          <p className="text-sm text-muted-foreground mb-2">Error ID: {id}</p>
        )}
        <p className="text-foreground">
          {message || 'An unexpected error occurred. Please try again.'}
        </p>
      </div>
    </div>
  );
}
