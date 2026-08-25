import type { OryClientConfiguration } from '@ory/elements-react';

const config: OryClientConfiguration = {
  sdk: {
    url: import.meta.env.VITE_KRATOS_URL || 'http://localhost:4433',
  },
  project: {
    hide_ory_branding: true,
    name: import.meta.env.VITE_APP_NAME || 'nutgraf',
    default_redirect_url: '/',
    error_ui_url: '/auth/error',
    login_ui_url: '/auth/login',
    registration_ui_url: '/auth/registration',
    verification_ui_url: '/auth/verification',
    recovery_ui_url: '/auth/recovery',
    settings_ui_url: '/settings',
    registration_enabled: true,
    verification_enabled: true,
    recovery_enabled: true,
  },
};

export default config;
