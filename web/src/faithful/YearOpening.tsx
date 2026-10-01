import {createContext,useEffect,useRef,useState} from 'react';
import {Logo} from '../Logo';

// The app's own address opens on the year, the way a phone opens on its home
// screen: the logo alone in the middle, light passing over it, then the camera
// dives in through the day tiles and every date of the calendar flies in from
// in front of the screen to settle in its place. Only loading the bare address
// plays it; /year, or Year in the sidebar, simply shows the calendar. Any key,
// click or scroll skips it.

/** logo: the logo shows while the year is read; dive: the camera goes
 * through it and the dates arrive. */
export type OpeningPhase='logo'|'dive';

/** Which part of the opening is playing, for the Year page to join in. */
export const OpeningContext=createContext<OpeningPhase|null>(null);

// The logo shows at least this long, so its light passes once whole, and the
// dive and the dates' arrival take this long after it.
const LOGO=1300,DIVE=1500;

/** Whether this page load opens with the logo: only one at the bare address
 * does, read before the app rewrites it to /year, and never for someone who
 * has asked for less motion. */
export function opensWithLogo(){
  return loadedAtRoot&&!matchMedia('(prefers-reduced-motion: reduce)').matches;
}
const loadedAtRoot=location.pathname==='/';

/** The opening's phase. It waits on the logo until the year is ready to
 * show, and is gone at once if the page fails or the app goes elsewhere. */
export function useOpening(start:boolean,ready:boolean,stop:boolean):OpeningPhase|null{
  const [phase,setPhase]=useState<OpeningPhase|null>(start?'logo':null);
  const began=useRef(performance.now());
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
    const events=['keydown','pointerdown','wheel','touchstart'] as const;
    for(const name of events)window.addEventListener(name,skip,{capture:true,passive:true});
    return()=>{for(const name of events)window.removeEventListener(name,skip,{capture:true})};
  },[playing]);
  return stop?null:phase;
}

/** The logo over the whole window, until the camera has gone through it. */
export function YearOpening({phase}:{phase:OpeningPhase}){
  return <div className={`opening ${phase}`} aria-hidden="true">
    <div className="openingstage"><Logo className="openinglogo" shine/></div>
  </div>;
}
