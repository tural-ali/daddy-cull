import {stillMotion} from './Celebration';
import {covered} from './keys';

// A file moved to the Bin shrinks to a dot where it was and drops into the
// Bin in the sidebar, so it is plain where it went and where to get it back.
// It is a small copy doing the flying: the page itself is never held up for
// it. With reduced motion, a photo open over the page, or no Bin in sight,
// nothing flies.

/** More than a handful of dots at once reads as noise, not as "these went there". */
const MOST=6;
const DOT=14;

function binIcon(){
  const icon=document.querySelector<HTMLElement>('#side a[href="/bin"] .icon');
  if(!icon?.checkVisibility({opacityProperty:true,visibilityProperty:true}))return null;
  const box=icon.getBoundingClientRect();
  if(!box.width||box.bottom<0||box.top>innerHeight||box.right<0||box.left>innerWidth)return null;
  return {icon,box};
}

function onScreen(box:DOMRect){return box.width>0&&box.bottom>0&&box.top<innerHeight&&box.right>0&&box.left<innerWidth}

let bump:Animation|null=null;
function land(icon:HTMLElement){
  bump?.cancel();
  bump=icon.animate([{scale:1},{scale:1.22,offset:.4},{scale:1}],{duration:320,easing:'cubic-bezier(.3,0,.2,1)'});
}

/** Sends a dot from each of these tiles to the Bin. Where the tiles are is
 * read at once, so they may leave the page straight after. */
export function flyToBin(tiles:(Element|null|undefined)[]){
  if(stillMotion()||covered())return;
  const bin=binIcon();
  if(!bin)return;
  const end={x:bin.box.left+bin.box.width/2,y:bin.box.top+bin.box.height/2};
  const starts=tiles.filter((tile):tile is Element=>!!tile?.isConnected)
    .map(tile=>({box:tile.getBoundingClientRect(),picture:tile.querySelector('img')?.currentSrc??''}))
    .filter(start=>onScreen(start.box)).slice(0,MOST);
  starts.forEach(({box,picture},index)=>{
    const dot=document.createElement('div');
    dot.className='binfly';
    dot.setAttribute('aria-hidden','true');
    const face=document.createElement('div');
    if(picture)face.style.backgroundImage=`url("${picture.replace(/"/g,'%22')}")`;
    dot.append(face);
    Object.assign(dot.style,{left:`${box.left}px`,top:`${box.top}px`,width:`${box.width}px`,height:`${box.height}px`});
    document.body.append(dot);
    const x=box.left+box.width/2,y=box.top+box.height/2;
    const delay=index*45;
    // First the picture draws in to a dot where it was, taking the Bin's colour.
    const shrink=dot.animate([
      {left:`${box.left}px`,top:`${box.top}px`,width:`${box.width}px`,height:`${box.height}px`,borderRadius:'8px'},
      {left:`${x-DOT/2}px`,top:`${y-DOT/2}px`,width:`${DOT}px`,height:`${DOT}px`,borderRadius:'50%'},
    ],{duration:200,delay,easing:'cubic-bezier(.4,0,.2,1)',fill:'forwards'});
    face.animate([{opacity:1},{opacity:1,offset:.45},{opacity:0}],{duration:200,delay,fill:'forwards'});
    // Then it is thrown in a low arc, gathering speed as it drops in.
    const dx=end.x-x,dy=end.y-y,distance=Math.hypot(dx,dy);
    const lift=Math.min(140,distance*.28);
    const control={x:x+dx*.5,y:Math.max(DOT,Math.min(y,end.y)-lift)};
    const steps=18,frames:Keyframe[]=[];
    for(let step=0;step<=steps;step++){
      const t=step/steps,u=1-t;
      const px=u*u*x+2*u*t*control.x+t*t*end.x,py=u*u*y+2*u*t*control.y+t*t*end.y;
      frames.push({translate:`${px-x}px ${py-y}px`,scale:t<.8?1:1-(t-.8)/.2*.5,opacity:t<.96?1:0});
    }
    const duration=Math.min(760,360+distance*.35);
    void shrink.finished.then(()=>{
      const fly=dot.animate(frames,{duration,easing:'cubic-bezier(.4,0,.75,.85)',fill:'forwards'});
      return fly.finished;
    }).then(()=>land(bin.icon)).catch(()=>{}).finally(()=>dot.remove());
  });
}
