import { Configuration, FrontendApi } from '@ory/client-fetch';

export const ory = new FrontendApi(
  new Configuration({
    basePath: import.meta.env.VITE_KRATOS_URL || 'http://localhost:4433',
    headers: { Accept: 'application/json' },
    credentials: 'include',
  })
);
