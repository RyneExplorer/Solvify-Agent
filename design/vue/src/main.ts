import { createApp } from 'vue'
import ElementPlus from 'element-plus'
import 'element-plus/dist/index.css'
import App from './App.vue'
import router from './router'
import { setRouter } from '@/api/client'
import './style.css'

const app = createApp(App)
app.use(ElementPlus, { size: 'small' })
app.use(router)
setRouter(router)
app.mount('#app')
