import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import vuetify from 'vite-plugin-vuetify'
import path from 'node:path'

export default defineConfig({
  plugins: [
    vue({ template: { transformAssetUrls: { base: null, includeAbsolute: false } } }),
    vuetify({ autoImport: true })
  ],
  resolve: {
    alias: { '@': path.resolve(__dirname, './src') }
  },
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: process.env.VITE_API_BASE_INTERNAL || 'http://localhost:8080',
        changeOrigin: true
      }
    }
  }
})
