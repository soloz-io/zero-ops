import React from 'react';
import ReactDOM from 'react-dom/client';
import { RouterProvider } from 'react-router-dom';
import { router } from './app/router';
import { OryProvider } from './contexts/ory-context';
import '@ory/elements-react/theme/styles.css';
import './app/globals.css';
import 'ai-design-system/dist/index.css';

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <OryProvider>
      <RouterProvider router={router} />
    </OryProvider>
  </React.StrictMode>
);
