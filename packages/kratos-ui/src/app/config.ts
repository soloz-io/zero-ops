import type { OryClientConfiguration } from '@ory/elements-react';

const config: OryClientConfiguration = {
  sdk: {
    // Same origin by default, and deliberately not a build-time literal.
    //
    // Vite inlines import.meta.env at BUILD time, so an unset VITE_KRATOS_URL used
    // to bake 'http://localhost:4433' into the shipped bundle — the browser then
    // tried to reach localhost from an HTTPS page and every flow failed before it
    // started. The Dockerfile passes VITE_APP_NAME but never passed this one.
    //
    // The page and Kratos are served from the same host: this app answers '/' and
    // the gateway routes '/self-service' to Kratos. Reading the origin at runtime
    // therefore needs no build argument, works in every environment without one,
    // and keeps the requests same-origin so no CORS grant is involved.
    //
    // The env var still wins when set, which is what local development uses to
    // point at a Kratos on localhost.
    url: import.meta.env.VITE_KRATOS_URL || window.location.origin,
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
