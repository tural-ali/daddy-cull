import {createContext,useEffect,useRef,useState,type CSSProperties} from 'react';
import {Logo} from '../Logo';

// The app's own address opens on the year, the way a phone opens on its home
// screen: the logo alone in the middle, then the camera
// dives in through the day tiles and every date of the calendar flies in from
// in front of the screen to settle in its place. Loading the year plays it,
// at the bare address or /year, a reload included; Year in the sidebar simply
// shows the calendar. Any key, click or scroll skips it.

/** logo: the logo shows while the year is read; dive: the camera goes
 * through it and the dates arrive. */
export type OpeningPhase='logo'|'dive';

/** Which part of the opening is playing, for the Year page to join in. */
export const OpeningContext=createContext<OpeningPhase|null>(null);

// The logo rests briefly so its full silhouette reads, and the
// dive and the dates' arrival take this long after it.
const LOGO=1200,CAMERA=1100,ARRIVE=950,LAND=850,STAGGER=180;
const DIVE=ARRIVE+LAND+STAGGER+40;
// Both the camera and the calendar use this clock, including the SVG fallback.
export const openingStyle={
  '--opening-camera':`${CAMERA}ms`,
  '--opening-clear':'1000ms',
  '--opening-arrive':`${ARRIVE}ms`,
  '--opening-land':`${LAND}ms`,
  '--opening-stagger':`${STAGGER}ms`,
} as CSSProperties;

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

/** Keep a plain SVG as the fallback while the optional 3D renderer loads. */
export function YearOpening({phase}:{phase:OpeningPhase}){
  const host=useRef<HTMLDivElement>(null);
  const scene=useRef<Awaited<ReturnType<typeof import('./logoScene')['logoScene']>>|null>(null);
  const [solid,setSolid]=useState(false);
  const diving=useRef<number|null>(null);
  useEffect(()=>{
    let active=true;
    const element=host.current!;
    const svg=element.querySelector('svg')!;
    void import('./logoScene').then(module=>{
      if(!active||diving.current!==null)return;
      scene.current=module.logoScene(element,svg);
      scene.current.draw(0);setSolid(true);
    }).catch(()=>{/* WebGL unavailable: the centred SVG push still reveals the year. */});
    return()=>{active=false;scene.current?.dispose();scene.current=null};
  },[]);
  useEffect(()=>{
    if(phase!=='dive')return;
    const began=performance.now();diving.current=began;
    if(!scene.current)return;
    let frame=0;
    const draw=(now:number)=>{
      const progress=(now-began)/CAMERA;
      scene.current?.draw(progress);
      if(progress<1)frame=requestAnimationFrame(draw);
    };
    // Follow the display's refresh rate, with no idle or finished-frame renders.
    frame=requestAnimationFrame(draw);
    return()=>cancelAnimationFrame(frame);
  },[phase]);
  return <div ref={host} className={`opening ${phase}${solid?' solid':''}`} style={openingStyle} aria-hidden="true">
    <div className="openingstage"><div className="openingworld"><Logo className="openinglogo"/></div></div>
  </div>;
}
