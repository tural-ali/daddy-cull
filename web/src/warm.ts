// Grid previews are lazy so the first screen of a page loads first. After that
// the rest are fetched in the background, a few at a time and in page order,
// so scrolling on after culling a stretch never waits for pictures. A few
// connections are always left free, so a decision or a full-size preview is
// never queued behind the grid.

const PARALLEL=4;
// A picture taken off the page before it loads fires nothing, so its slot is
// given back after this long.
const GIVE_UP_MS=20_000;

let running=0;
let queue:HTMLImageElement[]=[];
let scan=0;

function collect(){
  scan=0;
  queue=Array.from(document.querySelectorAll<HTMLImageElement>('img[loading="lazy"]:not([data-warm])'));
  pump();
}

function pump(){
  while(running<PARALLEL&&queue.length>0){
    const image=queue.shift()!;
    if(!image.isConnected||image.dataset.warm)continue;
    image.dataset.warm='';
    if(image.complete)continue;
    running++;
    let settled=false;
    const done=()=>{
      if(settled)return;
      settled=true;running--;clearTimeout(timer);
      image.removeEventListener('load',done);image.removeEventListener('error',done);
      pump();
    };
    const timer=window.setTimeout(done,GIVE_UP_MS);
    image.addEventListener('load',done);image.addEventListener('error',done);
    image.loading='eager';
  }
}

/** Starts once the page itself has loaded, and picks up every grid drawn
 * after that, whether a page's first data or the next batch. */
export function warmPreviews(){
  const start=()=>{
    new MutationObserver(()=>{if(!scan)scan=requestAnimationFrame(collect)}).observe(document.body,{childList:true,subtree:true});
    collect();
  };
  if(document.readyState==='complete')start();
  else window.addEventListener('load',start,{once:true});
}
