import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
export default defineConfig({
  plugins: [vue()],
  build: { outDir: '../webui/static', emptyOutDir: true, assetsDir: 'assets', sourcemap:false,
    rollupOptions:{output:{onlyExplicitManualChunks:true,manualChunks(id){
      const path=id.replaceAll('\\','/');
      if(path.includes('/components/RefundExecutionSettings.vue')||path.includes('/components/ResourceCheckSummary.vue')||path.includes('/components/AdminRefundExecution.vue'))return 'admin-execution';
      if(path.includes('/components/Admin.vue')||path.includes('/components/AdminAnnouncements.vue')||path.includes('/components/AdminAccountReports.vue')||path.includes('/components/AdminRefundMonitor.vue'))return 'admin-console';
    }}}
  },
  server: { host: '127.0.0.1', port: 18741, strictPort: true, proxy: { '/api': 'http://127.0.0.1:18740', '/open': 'http://127.0.0.1:18740', '/health': 'http://127.0.0.1:18740' } },
})
