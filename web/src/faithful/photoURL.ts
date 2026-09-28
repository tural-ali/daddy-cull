import {useCallback,useEffect,useRef,useState} from 'react';

// A preview has its own address, as in Google Photos: opening a photo adds
// /photo/<id> to the page's address, stepping to the next one rewrites it, and
// closing takes it off again. Back closes the preview, and reloading or
// sharing the address opens the same photo on the same page.

const PHOTO=/\/photo\/([^/]+)$/;

/** The page's own path, without an open photo. */
export function pagePath(path=location.pathname){return path.replace(PHOTO,'')||'/'}

function photoInAddress(){
  const match=location.pathname.match(PHOTO);
  return match?decodeURIComponent(match[1]):null;
}

function addressFor(id:string|null){
  const base=pagePath();
  return `${id===null?base:`${base==='/'?'':base}/photo/${encodeURIComponent(id)}`}${location.search}`;
}

/** The photo open on this page, read from and written to the address. `has`
 * says whether an id is on the page, or undefined while the page is still
 * loading its list; an address naming a photo that is not there, say one
 * decided since the link was made, just shows the page. A page that shows a
 * second grid below another's passes `enabled` false so the two never fight
 * over one address. */
export function usePhotoURL(has:(id:string)=>boolean|undefined,enabled=true){
  const [open,setOpen]=useState<string|null>(()=>{const id=enabled?photoInAddress():null;return id!==null&&has(id)!==false?id:null});
  // Whether this page added the history entry, so closing can go back to the
  // grid rather than leave a second copy of it behind.
  const pushed=useRef(false);
  // Closing goes back, which the browser does later. A photo opened before it
  // lands waits here for the grid's address, or the back would close it.
  const leaving=useRef(false);
  const waiting=useRef<string|null>(null);
  const latest=useRef(has);
  latest.current=has;
  const settled=useRef(false);
  const on=useRef(enabled);
  on.current=enabled;
  useEffect(()=>{
    if(!enabled)return;
    const pop=()=>{
      pushed.current=false;
      const opened=leaving.current?waiting.current:null;
      leaving.current=false;waiting.current=null;
      if(opened!==null&&photoInAddress()===null){history.pushState(history.state,'',addressFor(opened));pushed.current=true;return}
      const id=photoInAddress();setOpen(id!==null&&latest.current(id)!==false?id:null);
    };
    window.addEventListener('popstate',pop);
    return()=>window.removeEventListener('popstate',pop);
  },[enabled]);
  // The photo named by the address is checked once the page knows its list,
  // so this looks after every render until it has settled.
  // oxlint-disable-next-line react/exhaustive-deps -- runs until settled, see above
  useEffect(()=>{
    if(!enabled||settled.current)return;
    const id=photoInAddress();
    if(id===null){settled.current=true;return}
    const known=latest.current(id);
    if(known===undefined)return;
    settled.current=true;
    if(!known){setOpen(null);history.replaceState(history.state,'',addressFor(null))}
  });
  const show=useCallback((photo:string|number)=>{
    const id=String(photo);
    setOpen(id);
    if(!on.current)return;
    if(leaving.current){waiting.current=id;return}
    if(photoInAddress()===null){history.pushState(history.state,'',addressFor(id));pushed.current=true}
    else history.replaceState(history.state,'',addressFor(id));
  },[]);
  /** The preview stepped to another photo: the address follows it without
   * adding a history entry for every photo seen. */
  const moved=useCallback((photo:string|number)=>{
    const id=String(photo);
    if(leaving.current){if(waiting.current!==null)waiting.current=id;return}
    if(on.current&&photoInAddress()!==null&&photoInAddress()!==id)history.replaceState(history.state,'',addressFor(id));
  },[]);
  const close=useCallback(()=>{
    setOpen(null);
    waiting.current=null;
    if(!on.current||leaving.current||photoInAddress()===null)return;
    if(pushed.current){pushed.current=false;leaving.current=true;history.back()}
    else history.replaceState(history.state,'',addressFor(null));
  },[]);
  return {open,show,moved,close};
}
