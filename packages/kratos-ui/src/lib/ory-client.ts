import { Configuration, FrontendApi } from '@ory/client-fetch';
import { kratosBaseUrl } from './kratos-url';

export const ory = new FrontendApi(
  new Configuration({
    basePath: kratosBaseUrl,
    headers: { Accept: 'application/json' },
    credentials: 'include',
  })
);
