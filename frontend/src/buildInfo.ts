export type FrontendBuildInfo = {
  version: string
  commit: string
  buildTime: string
}

export const frontendBuildInfo: FrontendBuildInfo = {
  version: import.meta.env.VITE_APP_VERSION || 'v0.43.0-dev',
  commit: import.meta.env.VITE_APP_COMMIT || 'unknown',
  buildTime: import.meta.env.VITE_APP_BUILD_TIME || 'unknown'
}
