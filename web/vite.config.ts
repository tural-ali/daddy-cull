import {defineConfig} from 'vite';

export default defineConfig({
  server:{proxy:{'/api':'http://127.0.0.1:8830'}},
  build:{rollupOptions:{input:{app:new URL('index.html',import.meta.url).pathname,queue:new URL('queue.html',import.meta.url).pathname}}},
});
