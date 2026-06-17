/**
 * Vite build-time globals used by the desktop frontend.
 *
 * Responsibilities:
 *   - Declare constants injected by desktop/vite.config.ts.
 *   - Keep component code type-safe without importing build metadata at runtime.
 *
 * Boundaries:
 *   - Does not define runtime environment variables.
 *   - Does not read package or release metadata directly.
 */
declare const __SUPERDEV_RELEASE_BASE_URL__: string
