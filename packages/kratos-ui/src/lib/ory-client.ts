import { Configuration, FrontendApi } from '@ory/client-fetch';

// Kratos and this SPA share the same origin (console.dev.nutgraf.in).
// The gateway routes /self-service/* → ory-kratos-public, everything else → this SPA.
// Using window.location.origin means no CORS issues and no hardcoded URLs.
export const ory = new FrontendApi(
  new Configuration({
    basePath: window.location.origin,
    headers: { Accept: 'application/json' },
    credentials: 'include',
  })
);
