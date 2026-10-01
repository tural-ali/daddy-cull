import {createContext,useEffect,useRef,useState,type CSSProperties} from 'react';
import {Logo,logoTilePoints} from '../Logo';

// The app's own address opens on the year, the way a phone opens on its home
// screen: the logo alone in the middle, light passing over it, then the camera
// dives in through the day tiles and every date of the calendar flies in from
// in front of the screen to settle in its place. Loading the year plays it,
// at the bare address or /year, a reload included; Year in the sidebar simply
// shows the calendar. Any key, click or scroll skips it.

/** logo: the logo shows while the year is read; dive: the camera goes
 * through it and the dates arrive. */
export type OpeningPhase='logo'|'dive';

/** Which part of the opening is playing, for the Year page to join in. */
export const OpeningContext=createContext<OpeningPhase|null>(null);

// The logo shows at least this long, so its light passes once whole, and the
// dive and the dates' arrival take this long after it.
const LOGO=1650,DIVE=2550;

/** Whether this page load opens with the logo: one at the bare address or
 * /year does, read before the app rewrites the one to the other, and never
 * for someone who has asked for less motion. */
export function opensWithLogo(){
  return loadedAtRoot&&!matchMedia('(prefers-reduced-motion: reduce)').matches;
}
const loadedAtRoot=location.pathname==='/'||location.pathname==='/year';

/** The opening's phase. It waits on the logo until the year is ready to
 * show, and is gone at once if the page fails or the app goes elsewhere. */
export function useOpening(start:boolean,ready:boolean,stop:boolean):OpeningPhase|null{
  const [phase,setPhase]=useState<OpeningPhase|null>(start?'logo':null);
  const began=useRef(performance.now());
  useEffect(()=>{if(stop)setPhase(null)},[stop]);
  useEffect(()=>{
    if(phase!=='logo'||!ready)return;
    const timer=setTimeout(()=>setPhase('dive'),Math.max(0,LOGO-(performance.now()-began.current)));
    return()=>clearTimeout(timer);
  },[phase,ready]);
  useEffect(()=>{
    if(phase!=='dive')return;
    const timer=setTimeout(()=>setPhase(null),DIVE);
    return()=>clearTimeout(timer);
  },[phase]);
  const playing=phase!==null;
  useEffect(()=>{
    if(!playing)return;
    const skip=()=>setPhase(null);
    const motion=matchMedia('(prefers-reduced-motion: reduce)');
    motion.addEventListener('change',skip);
    const events=['keydown','pointerdown','wheel','touchstart'] as const;
    for(const name of events)window.addEventListener(name,skip,{capture:true,passive:true});
    return()=>{
      motion.removeEventListener('change',skip);
      for(const name of events)window.removeEventListener(name,skip,{capture:true});
    };
  },[playing]);
  return stop?null:phase;
}

/** The same outlines as the SVG, extruded with front, back and edge faces.
 * All dimensions share an em unit so the geometry scales with the wordmark. */
function OpeningMark(){
  return <div className="openingmark"><div className="openingcamera">
    {logoTilePoints.map((points,index)=><div className={`openingsolid solid${index}`} key={index}>
      <div className="openingface back" style={{clipPath:`polygon(${points.map(([x,y])=>`${x/184*100}% ${y/104*100}%`).join(',')})`}}/>
      {points.map(([x,y],edge)=>{
        const [nextX,nextY]=points[(edge+1)%points.length];
        const angle=Math.atan2(nextY-y,nextX-x);
        return <div className="openingedge" key={edge} style={{
          width:`${Math.hypot(nextX-x,nextY-y)}em`,
          transform:`translate3d(${x}em,${y}em,-12em) rotateZ(${angle}rad) rotateX(90deg)`,
          '--edge-light':`${18+Math.round((Math.sin(angle)+1)*14)}%`,
        } as CSSProperties}/>;
      })}
      <div className="openingface front" style={{clipPath:`polygon(${points.map(([x,y])=>`${x/184*100}% ${y/104*100}%`).join(',')})`}}/>
    </div>)}
  </div></div>;
}

/** The logo over the whole window, until the camera has gone through it. */
export function YearOpening({phase}:{phase:OpeningPhase}){
  return <div className={`opening ${phase}`} aria-hidden="true">
    <div className="openingstage"><div className="openingworld"><Logo className="openinglogo" shine/><OpeningMark/></div></div>
  </div>;
}
