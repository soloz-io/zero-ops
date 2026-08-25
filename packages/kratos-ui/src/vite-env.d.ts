/// <reference types="vite/client" />

/**
 * Build-time configuration surfaced by Vite.
 *
 * Declared explicitly (rather than relying on the ambient `vite/client` index
 * signature) so a typo in a flag name is a compile error. `build` runs
 * `tsc --noEmit` first, so this is enforced before a bundle is produced.
 */
interface ImportMetaEnv {
  /**
   * The Ory Kratos public URL for the SPA to communicate with.
   */
  readonly VITE_KRATOS_URL: string;

  /**
   * The application name displayed in the auth card header.
   */
  readonly VITE_APP_NAME?: string;

  /**
   * Enables the local development identity in AuthProvider. Only honoured in a DEV
   * build served from a local hostname (ADR-050 P1-7) — a production bundle cannot
   * enable it at all.
   */
  readonly VITE_DEV_AUTH?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
