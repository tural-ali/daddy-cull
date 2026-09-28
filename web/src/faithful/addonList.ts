import {useSyncExternalStore} from 'react';
import {failure} from '../api';

// Everything beyond dates, duplicates and the Bin is an addon, which can be
// turned off in one place. The list is read once for the whole page and
// again whenever the server says one was turned on or off, here or in
// another tab, so the sidebar only offers what is on.

export type AddonSection='collections'|'sync'|'tools';
export type AddonPage={id:string;label:string;icon:string;section:AddonSection;path:string;url?:string};
export type AddonState='ready'|'setup'|'problem';
export type Addon={
  id:string;name:string;version:string;summary:string;description?:string;author?:string;homepage?:string;icon?:string;
  pages?:AddonPage[];permissions?:string[];needs?:string[];work?:string[];
  builtIn:boolean;on:boolean;chosen:boolean;status:{state:AddonState;detail:string};routes:number;folder?:string;problem?:string;
};

let addons:Addon[]|null=null;
/** The list could not be read, so the frame shows what it always had. */
let unknown=false;
const listeners=new Set<()=>void>();
let reading:Promise<void>|null=null;
let events:EventSource|null=null;

function publish(next:Addon[]|null){
  addons=next;
  for(const listener of listeners)listener();
}

/** Reads the list again. A read already on its way is shared. */
export function readAddons():Promise<void>{
  reading??=(async()=>{
    try{
      const response=await fetch('/api/addons',{cache:'no-store'});
      if(!response.ok)throw new Error(String(response.status));
      unknown=false;
      publish(await response.json() as Addon[]);
    }catch{
      // A list that cannot be read leaves the last one standing, or, before
      // there is one, says so, and the frame offers every page of Cull's own.
      if(addons===null&&!unknown){unknown=true;publish(null)}
    }finally{reading=null}
  })();
  return reading;
}

function follow(){
  if(events||typeof EventSource==='undefined')return;
  events=new EventSource('/api/events');
  events.addEventListener('addons',()=>void readAddons());
}

function subscribe(listener:()=>void){
  listeners.add(listener);
  if(listeners.size===1){
    void readAddons();
    follow();
    document.addEventListener('visibilitychange',visible);
  }
  return()=>{
    listeners.delete(listener);
    if(listeners.size>0)return;
    events?.close();events=null;
    document.removeEventListener('visibilitychange',visible);
  };
}
// A tab left in the background may have missed a change while its stream
// was asleep.
function visible(){if(!document.hidden)void readAddons()}

/** The list as last read, for code outside React. */
export function currentAddons(){return addons}

/** Every addon, or null until the list is read, or when it cannot be. */
export function useAddons(){return useSyncExternalStore(subscribe,()=>addons)}

/** The addon that owns a page of Cull's own, by its path. */
export function ownerOf(list:Addon[]|null,path:string){
  return list?.find(addon=>addon.builtIn&&addon.pages?.some(page=>page.path===path));
}

/** An addon of your own and its page, for /addons/<addon>/<page>. */
export function framePage(list:Addon[]|null,path:string){
  const [,,id,pageId]=path.split('/');
  const addon=list?.find(item=>item.id===id&&!item.builtIn);
  const page=addon?.pages?.find(item=>item.id===pageId);
  return addon&&page?{addon,page}:null;
}

/** Turns an addon on or off, and says what it is now. */
export async function setAddon(id:string,on:boolean):Promise<Addon>{
  const response=await fetch(`/api/addons/${encodeURIComponent(id)}`,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({on})});
  if(!response.ok)throw new Error(await failure(response,`The addon could not be turned ${on?'on':'off'}.`));
  const saved=await response.json() as Addon;
  if(addons)publish(addons.map(addon=>addon.id===id?saved:addon));
  return saved;
}
