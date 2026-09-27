// Graduation adds files to the archive at night, from another process, so a
// page left open overnight goes on showing the day before. The server counts
// every change to what the catalogue holds (/api/catalogue); the frame reads
// that count once a minute and whenever the tab comes back into view, and
// when it has moved the page can be read again.

/** Announced when the catalogue changed under the open page, for anything
 * that shows counts from it. */
export const CATALOGUE_CHANGED='cull:catalogue-changed';

/** How often an open page asks whether the archive changed under it. */
export const CATALOGUE_CHECK_MS=60_000;

export async function catalogueGeneration():Promise<number|null>{
  try{
    const response=await fetch('/api/catalogue',{cache:'no-store'});
    if(!response.ok)return null;
    return (await response.json() as {generation:number}).generation;
  }catch{return null}
}

/** Whether the page can be read again without anyone losing anything: no
 * photo open, no dialog, nothing being typed and no decision still on its way
 * to the server. Whether the page holds an undo history is the caller's to
 * know. */
export function quietEnough(){
  if(/\/photo\/[^/]+$/.test(location.pathname))return false;
  if(document.querySelector('dialog[open]'))return false;
  const active=document.activeElement;
  if(active instanceof HTMLInputElement||active instanceof HTMLTextAreaElement||active instanceof HTMLSelectElement||(active instanceof HTMLElement&&active.isContentEditable))return false;
  try{
    if(Object.keys(localStorage).some(key=>(key.startsWith('cull.pending.')||key.startsWith('cull.group.pending.'))&&waiting(localStorage.getItem(key))))return false;
  }catch{/* No storage, so nothing can be waiting in it. */}
  return true;
}

// A tab's journal stays behind as an empty list once its last choice is
// saved; only a journal with something in it, or one that cannot be read,
// is a choice still on its way.
function waiting(raw:string|null){
  if(!raw)return false;
  try{const jobs:unknown=JSON.parse(raw);return !Array.isArray(jobs)||jobs.length>0}catch{return true}
}

/** Calls check once a minute and whenever the tab comes back into view. */
export function watchCatalogue(check:()=>void):()=>void{
  const timer=window.setInterval(check,CATALOGUE_CHECK_MS);
  const visible=()=>{if(!document.hidden)check()};
  document.addEventListener('visibilitychange',visible);
  window.addEventListener('focus',visible);
  return()=>{window.clearInterval(timer);document.removeEventListener('visibilitychange',visible);window.removeEventListener('focus',visible)};
}
