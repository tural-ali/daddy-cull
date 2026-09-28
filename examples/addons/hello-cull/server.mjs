// Hello, Cull: the smallest addon with a page. It serves the page Cull shows
// in its frame, and asks Cull for the library's counts on the page's behalf,
// so the addon's key stays on this machine and never reaches a browser.
//
//   CULL_URL=http://127.0.0.1:8830 node server.mjs
//
// Node 18 or later, nothing to install.

import {createServer} from 'node:http';
import {readFile} from 'node:fs/promises';
import {dirname,join} from 'node:path';
import {fileURLToPath} from 'node:url';

const here=dirname(fileURLToPath(import.meta.url));
const cull=new URL(process.env.CULL_URL??'http://127.0.0.1:8830');
const port=Number(process.env.PORT??8900);
const host=process.env.HOST??'127.0.0.1';

// Cull writes the key when the addon is first turned on, so it is read
// afresh each time rather than once at start.
async function key(){
  try{return (await readFile(join(here,'key'),'utf8')).trim()}catch{return ''}
}

async function stats(){
  const secret=await key();
  if(!secret)return {status:409,body:{error:'Turn Hello, Cull on in Cull’s Addons page first. That writes its key.'}};
  const response=await fetch(new URL('/api/stats',cull),{headers:{Authorization:`Bearer ${secret}`}});
  return {status:response.status,body:await response.json()};
}

const page=`<!doctype html>
<html lang="en-GB">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Hello, Cull</title>
<style>
  :root { --background: #121317; --surface: #18191e; --text: #eef0f5; --muted: #9297a3; --line: #282b34; --accent: #7ed9b2; --onAccent: #0b2e20; color-scheme: dark; }
  :root[data-theme="light"] { color-scheme: light; }
  body { margin: 0; padding: 24px; background: var(--surface); color: var(--text); font: 15px/1.5 system-ui, sans-serif; }
  h1 { margin: 0 0 4px; font-size: 22px; font-weight: 500; }
  p { margin: 0 0 20px; color: var(--muted); }
  dl { display: grid; grid-template-columns: repeat(auto-fill, minmax(150px, 1fr)); gap: 12px; margin: 0 0 24px; }
  dl div { padding: 14px 16px; border: 1px solid var(--line); border-radius: 12px; }
  dt { color: var(--muted); font-size: 13px; }
  dd { margin: 2px 0 0; font-size: 26px; font-variant-numeric: tabular-nums; }
  button { height: 36px; padding: 0 16px; border: 0; border-radius: 18px; background: var(--accent); color: var(--onAccent); font: inherit; font-weight: 500; cursor: pointer; }
</style>
</head>
<body>
<h1>Hello, Cull</h1>
<p id="said">Asking Cull what the library holds…</p>
<dl id="counts" hidden></dl>
<button type="button" id="bin">Open the Bin</button>
<script>
  // Cull says which theme it is in, and its colours, when the page loads
  // and whenever the theme changes. Only Cull's own messages are heard.
  const cull=${JSON.stringify(cull.origin)};
  addEventListener('message',event=>{
    if(event.origin!==cull||!event.data)return;
    if(event.data.type==='cull:hello'||event.data.type==='cull:theme'){
      document.documentElement.dataset.theme=event.data.theme;
      for(const [name,value] of Object.entries(event.data.colors??{}))if(value)document.documentElement.style.setProperty('--'+name,value);
    }
  });
  // Asking Cull to go to one of its own pages.
  document.getElementById('bin').onclick=()=>parent.postMessage({type:'cull:navigate',path:'/bin'},cull);
  const counts=[['total','Files'],['decisions','Choices'],['favourites','Hearts'],['bin','In the Bin']];
  fetch('/stats').then(async response=>{
    const body=await response.json();
    if(!response.ok)throw new Error(body.error??'Cull did not answer.');
    const list=document.getElementById('counts');
    for(const [field,label] of counts){
      const row=document.createElement('div'),name=document.createElement('dt'),value=document.createElement('dd');
      name.textContent=label;
      value.textContent=Number(body[field]??0).toLocaleString('en-GB');
      row.append(name,value);
      list.append(row);
    }
    list.hidden=false;
    document.getElementById('said').textContent='What the library holds now.';
  }).catch(reason=>{document.getElementById('said').textContent=reason.message});
</script>
</body>
</html>`;

createServer(async(request,response)=>{
  const path=new URL(request.url??'/','http://addon').pathname;
  try{
    if(request.method==='GET'&&path==='/'){
      // Cull may show this page in its frame; nothing else may.
      response.writeHead(200,{'Content-Type':'text/html; charset=utf-8','Content-Security-Policy':`frame-ancestors ${cull.origin}`});
      response.end(page);
      return;
    }
    if(request.method==='GET'&&path==='/stats'){
      const {status,body}=await stats();
      response.writeHead(status,{'Content-Type':'application/json'});
      response.end(JSON.stringify(body));
      return;
    }
    response.writeHead(404,{'Content-Type':'application/json'});
    response.end(JSON.stringify({error:'Nothing is here.'}));
  }catch(error){
    response.writeHead(502,{'Content-Type':'application/json'});
    response.end(JSON.stringify({error:`Cull could not be reached at ${cull.origin}: ${error.message}`}));
  }
}).listen(port,host,()=>console.log(`Hello, Cull is at http://${host}:${port}/, talking to ${cull.origin}`));
