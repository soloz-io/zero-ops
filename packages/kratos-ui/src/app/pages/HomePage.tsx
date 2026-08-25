import { useOrySession } from '@/contexts/ory-context';

export default function HomePage() {
  const { session, loading, logout } = useOrySession();

  if (loading) {
    return (
      <div className="flex min-h-svh w-full flex-col items-center justify-center p-6 md:p-10 bg-background text-foreground">
        <div>Loading...</div>
      </div>
    );
  }

  if (!session) {
    return (
      <div className="flex min-h-svh w-full flex-col items-center justify-center p-6 md:p-10 bg-background text-foreground">
        <h1 className="text-2xl font-bold mb-4">Not authenticated</h1>
        <a href="/auth/login" className="text-primary underline">
          Sign in
        </a>
      </div>
    );
  }

  return (
    <div className="flex min-h-svh w-full flex-col items-center justify-center p-6 md:p-10 bg-background text-foreground">
      <h1 className="text-2xl font-bold mb-4">Welcome</h1>
      <p className="mb-4">
        Signed in as <strong>{session.identity.traits.email as string}</strong>
      </p>
      <button
        onClick={() => logout()}
        className="px-4 py-2 bg-destructive text-destructive-foreground rounded"
      >
        Sign out
      </button>
    </div>
  );
}
