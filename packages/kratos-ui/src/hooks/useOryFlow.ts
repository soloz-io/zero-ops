import { useState, useEffect } from 'react';
import { useSearchParams } from 'react-router-dom';
import { ory } from '@/lib/ory-client';
import { selfServiceBrowserUrl } from '@/lib/kratos-url';

export function useOryFlow(flowType: string) {
  const [searchParams] = useSearchParams();
  const [flow, setFlow] = useState<any | null>(null);
  const [error, setError] = useState<Error | null>(null);

  useEffect(() => {
    const flowId = searchParams.get('flow');
    const returnTo = searchParams.get('return_to') || undefined;

    if (flowId) {
      const fetchFlow = async () => {
        try {
          let res;
          switch (flowType) {
            case 'login':
              res = await ory.getLoginFlow({ id: flowId });
              break;
            case 'registration':
              res = await ory.getRegistrationFlow({ id: flowId });
              break;
            case 'verification':
              res = await ory.getVerificationFlow({ id: flowId });
              break;
            case 'recovery':
              res = await ory.getRecoveryFlow({ id: flowId });
              break;
            case 'settings':
              res = await ory.getSettingsFlow({ id: flowId });
              break;
            default:
              throw new Error(`Unsupported flow type: ${flowType}`);
          }
          setFlow(res);
        } catch (err: any) {
          if (err?.response?.status === 410 || err?.status === 410) {
            window.location.replace(selfServiceBrowserUrl(flowType, returnTo));
          } else {
            setError(err);
          }
        }
      };
      fetchFlow();
    } else {
      window.location.replace(selfServiceBrowserUrl(flowType, returnTo));
    }
  }, [flowType, searchParams]);

  return { flow, error };
}
