import {useSyncExternalStore} from 'react';

// The catalogue names every library file under /archive/, whatever folder
// holds the library. A path shown or copied names that folder instead, as
// Finder and Terminal know it, once Setup has said where it is.
let library='';
const listeners=new Set<()=>void>();
let asked=false;

function learn(){
  if(asked)return;
  asked=true;
  fetch('/api/setup',{cache:'no-store'}).then(response=>response.ok?response.json() as Promise<{config?:{library?:string}}>:null).then(view=>{
    const folder=(view?.config?.library||'').replace(/\/+$/,'');
    if(!folder||folder===library)return;
    library=folder;
    listeners.forEach(listener=>listener());
  },()=>{/* the catalogue's own names are shown instead */});
}

/** The path of a library file on this computer, or the catalogue's own name
* for it until the library folder is known. */
export function shownPath(path:string){
  learn();
  return library&&path.startsWith('/archive/')?library+path.slice('/archive'.length):path;
}

function subscribe(listener:()=>void){
  learn();
  listeners.add(listener);
  return ()=>{listeners.delete(listener)};
}

/** shownPath, for a component that shows paths again once the folder is known. */
export function useShownPath(){
  useSyncExternalStore(subscribe,()=>library);
  return shownPath;
}
