import {useEffect,useLayoutEffect,useRef,useState} from 'react';
import {Snacks} from './Snacks';

// Cmd+ and Cmd- make the thumbnails bigger or smaller instead of the whole
// page, as Google Photos does, and Cmd 0 puts them back. The size is one
// factor, --zoom, that every grid's tile size is multiplied by, remembered
// on this device. Pages without a grid leave the browser's own zoom alone.

const levels=[.5,.625,.75,.875,1,1.25,1.5,1.75,2,2.5];
const normal=levels.indexOf(1);
const key='cull.grid-zoom';
const mac=/Mac|iPhone|iPad/.test(navigator.platform);

function saved(){
  try{const level=levels.indexOf(Number(localStorage.getItem(key)));return level<0?normal:level}catch{return normal}
}

/** The tile nearest the middle of the screen, and how far down the screen it
 * is, so the grid can reflow around it. The middle itself is often a gap
 * between tiles, so the nearest one is looked for rather than hit. */
function anchor(){
  const main=document.querySelector('main');
  if(!main)return null;
  const box=main.getBoundingClientRect();
  const x=box.left+box.width/2,y=window.innerHeight/2;
  let best:{tile:Element;top:number}|null=null,distance=Infinity;
  for(const tile of main.querySelectorAll('figure, .dupetile')){
    const rect=tile.getBoundingClientRect();
    if(rect.bottom<0||rect.top>window.innerHeight||!rect.width)continue;
    const dx=Math.max(rect.left-x,0,x-rect.right),dy=Math.max(rect.top-y,0,y-rect.bottom);
    const d=dx*dx+dy*dy;
    if(d<distance){distance=d;best={tile,top:rect.top}}
  }
  return best;
}

/** Listens for the keys on pages with a grid, and says the new size. */
export function GridZoom({enabled}:{enabled:boolean}){
  const [level,setLevel]=useState(saved);
  const now=useRef(level);
  now.current=level;
  const [told,setTold]=useState(0);
  const held=useRef<ReturnType<typeof anchor>>(null);
  useLayoutEffect(()=>{
    document.documentElement.style.setProperty('--zoom',String(levels[level]));
    const kept=held.current;
    held.current=null;
    if(kept?.tile.isConnected)window.scrollBy(0,kept.tile.getBoundingClientRect().top-kept.top);
  },[level]);
  useEffect(()=>{
    if(!enabled)return;
    function onKey(event:KeyboardEvent){
      if(!(mac?event.metaKey:event.ctrlKey)||event.altKey)return;
      const by=event.key==='='||event.key==='+'?1:event.key==='-'||event.key==='_'?-1:event.key==='0'?0:null;
      if(by===null)return;
      // A photo open in the viewer is looked at, not the grid behind it: the
      // browser zooms as it always does.
      if(document.querySelector('[aria-modal="true"]'))return;
      event.preventDefault();
      const next=by===0?normal:Math.min(levels.length-1,Math.max(0,now.current+by));
      if(next!==now.current){
        held.current=anchor();
        try{if(next===normal)localStorage.removeItem(key);else localStorage.setItem(key,String(levels[next]))}catch{/* kept for this visit only */}
        setLevel(next);
      }
      setTold(count=>count+1);
    }
    window.addEventListener('keydown',onKey);
    return()=>window.removeEventListener('keydown',onKey);
  },[enabled]);
  useEffect(()=>{
    if(!told)return;
    const timer=setTimeout(()=>setTold(0),1800);
    return()=>clearTimeout(timer);
  },[told,level]);
  if(!told||!enabled)return null;
  const reset=mac?'⌘0':'Ctrl+0';
  const size=level===levels.length-1?'Largest thumbnails':level===0?'Smallest thumbnails':`Thumbnails at ${Math.round(levels[level]*100)}%`;
  return <Snacks><div className="snack" role="status">{size}{level!==normal&&<span className="dim"> · {reset} resets</span>}</div></Snacks>;
}
