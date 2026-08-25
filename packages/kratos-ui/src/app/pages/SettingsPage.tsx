import { Settings } from '@ory/elements-react/theme';
import { useOryFlow } from '@/hooks/useOryFlow';
import oryConfig from '../config';

export default function SettingsPage() {
  const { flow, error } = useOryFlow('settings');

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
      <Settings flow={flow} config={oryConfig} />
    </div>
  );
}
