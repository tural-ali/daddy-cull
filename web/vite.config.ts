import {defineConfig} from 'vite';

export default defineConfig({
  server:{proxy:{'/api':'http://127.0.0.1:8830'}},
  // The browser tests serve the build with vite preview and answer the API
  // themselves. What they leave unanswered fails here, and never reaches a
  // Daddy Cull running on this computer.
  preview:{proxy:{}},
  plugins:[{name:'no-api-in-preview',configurePreviewServer(server){
    server.middlewares.use('/api',(_request,response)=>{response.statusCode=502;response.end()});
  }}],
  build:{rollupOptions:{input:{app:new URL('index.html',import.meta.url).pathname}}},
});
