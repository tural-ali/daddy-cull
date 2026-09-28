// The app moves between its pages without reloading, so the top bar and the
// sidebar stay where they are and only the panel beside them changes. Each
// page visit is its own history entry, told apart by a number kept in the
// entry's state: a photo opened on a page rewrites the address but keeps that
// number, so Back closes the photo rather than reloading the page. Where the
// page was scrolled to is kept in its entry too, for Back and Forward.

/** Sent when the app has moved to another page. */
export const NAVIGATED='cull:navigated';
/** Sent by a page that needs itself read again in place, say after an undo
 * the server applied. */
export const RELOAD_PAGE='cull:reload-page';

type Entry={page:number;scroll?:number};

// Numbers only need to differ within one tab's history, which survives a
// reload, so they start from the clock rather than from one.
let counter=Date.now();

function entry():Entry|null{
  const state=history.state as Partial<Entry>|null;
  return state&&typeof state.page==='number'?state as Entry:null;
}

if(!entry())history.replaceState({...(history.state as object|null),page:++counter},'');
if('scrollRestoration' in history)history.scrollRestoration='manual';

/** The number of the page visit the browser is on. */
export function currentVisit(){return entry()?.page??0}

/** Where the current visit was scrolled to when it was left, if it was. */
export function savedScroll(){return entry()?.scroll}

// Every address the app draws itself; anything else, such as a file under
// /api, is left to the browser.
const APP=/^\/(?:on\/\d\d-\d\d|day\/\d{4}-\d\d-\d\d|year|duplicates|upgrades|shadows|screenshots|social|photos|log|bin|settings)?(?:\/photo\/[^/]+)?\/?$/;

export function isAppPath(path:string){return APP.test(path)}

/** Goes to another page of the app. The same address again replaces the
 * entry rather than adding one, and reads the page afresh. */
export function navigate(to:string){
  const url=new URL(to,location.href);
  if(url.origin!==location.origin||!isAppPath(url.pathname)){location.assign(url.href);return}
  const address=url.pathname+url.search+url.hash;
  history.replaceState({...(history.state as object|null),scroll:scrollY},'');
  const next:Entry={page:++counter};
  if(address===location.pathname+location.search+location.hash)history.replaceState(next,'',address);
  else history.pushState(next,'',address);
  window.dispatchEvent(new Event(NAVIGATED));
}

/** Asks the app to read the current page again where it is. */
export function reloadPage(){window.dispatchEvent(new Event(RELOAD_PAGE))}

// A plain click on a link to one of the app's pages moves within the app. A
// click with a modifier, a link meant for another tab or a download, and a
// link to a place further down the same page are the browser's.
function follow(event:MouseEvent){
  if(event.defaultPrevented||event.button!==0||event.metaKey||event.ctrlKey||event.shiftKey||event.altKey)return;
  const link=event.target instanceof Element?event.target.closest('a[href]'):null;
  if(!(link instanceof HTMLAnchorElement)||link.hasAttribute('download')||(link.target&&link.target!=='_self'))return;
  const url=new URL(link.href);
  if(url.origin!==location.origin||!isAppPath(url.pathname))return;
  if(url.hash&&url.pathname===location.pathname&&url.search===location.search)return;
  event.preventDefault();
  navigate(url.pathname+url.search+url.hash);
}

/** Starts following the app's own links, and returns how to stop. */
export function followLinks(){
  document.addEventListener('click',follow);
  return()=>document.removeEventListener('click',follow);
}
