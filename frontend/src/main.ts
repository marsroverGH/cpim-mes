import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { createVuetify } from 'vuetify'
import 'vuetify/styles'
import '@mdi/font/css/materialdesignicons.css'

import App from './App.vue'
import router from './router'

// Components and directives are imported per use by vite-plugin-vuetify
// (autoImport in vite.config.ts). Registering `vuetify/components` here would
// pull every Vuetify component into the entry chunk.
const vuetify = createVuetify({
  theme: {
    defaultTheme: 'cpimLight',
    themes: {
      cpimLight: {
        dark: false,
        colors: {
          primary: '#1565C0',
          secondary: '#37474F',
          accent: '#00838F',
          error:   '#C62828',
          warning: '#EF6C00',
          info:    '#1976D2',
          success: '#2E7D32'
        }
      }
    }
  }
})

createApp(App).use(createPinia()).use(router).use(vuetify).mount('#app')
