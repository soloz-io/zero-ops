import { Login } from '@ory/elements-react/theme';
import { useOryFlow } from '@/hooks/useOryFlow';
import oryConfig from '../config';

export default function LoginPage() {
  const { flow, error } = useOryFlow('login');

  if (error) {
    return (
      <div className="flex min-h-svh w-full flex-col items-center justify-center p-6 md:p-10 bg-background text-foreground">
        <div className="text-destructive">Something went wrong. Please try again.</div>
      </div>
    );
  }

  if (!flow) return null;

  return (
    <div className="flex min-h-svh w-full flex-col items-center justify-center p-6 md:p-10 bg-background text-foreground">
      <Login flow={flow} config={oryConfig} />
    </div>
  );
}
