/// <reference types="vite/client" />

declare module '*.vue' {
  import type { DefineComponent } from 'vue'
  const component: DefineComponent<{}, {}, any>
  export default component
}

interface ImportMetaEnv {
  readonly VITE_API_BASE: string
  readonly VITE_APP_VERSION: string
  readonly VITE_APP_COMMIT: string
  readonly VITE_APP_BUILD_TIME: string
}
interface ImportMeta {
  readonly env: ImportMetaEnv
}
