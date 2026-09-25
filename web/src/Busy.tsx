import {useLayoutEffect,useRef,useState} from 'react';
import {ThinkingOrb} from 'thinking-orbs';

export type OrbState='working'|'searching'|'solving'|'listening'|'connecting'|'weaving'|'composing'|'breathing'|'shaping';

type Theme='dark'|'light';

// One reusable pixel. Painting a computed colour and reading it back turns any
// CSS colour syntax the browser may hand over (rgb, oklab, color-mix results)
// into plain sRGB bytes without a parser of our own.
let probe:CanvasRenderingContext2D|null=null;
function rgba(css:string):[number,number,number,number]|null{
  if(!probe){const canvas=document.createElement('canvas');canvas.width=canvas.height=1;probe=canvas.getContext('2d',{willReadFrequently:true})}
  if(!probe)return null;
  probe.clearRect(0,0,1,1);probe.fillStyle='#000';probe.fillStyle=css;probe.fillRect(0,0,1,1);
  const [r,g,b,a]=probe.getImageData(0,0,1,1).data;
  return [r,g,b,a/255];
}
function luminance([r,g,b]:[number,number,number,number]){
  const linear=(value:number)=>{const c=value/255;return c<=.04045?c/12.92:((c+.055)/1.055)**2.4};
  return .2126*linear(r)+.7152*linear(g)+.0722*linear(b);
}
// The orb draws light dots for a dark surface and dark dots for a light one.
// The page follows the system theme, but a waiting state also sits inside
// surfaces that do not: a white-on-blue button, or the always-dark controls
// laid over a photograph. So the choice is made where the orb actually is:
// words lighter than the first opaque background above them mean a dark surface.
function themeFor(element:HTMLElement):Theme|null{
  const ink=rgba(getComputedStyle(element).color);
  if(!ink)return null;
  for(let node:HTMLElement|null=element;node;node=node.parentElement){
    const surface=rgba(getComputedStyle(node).backgroundColor);
    if(surface&&surface[3]>.5)return luminance(ink)>luminance(surface)?'dark':'light';
  }
  return null;
}

/** A waiting state: a thinking orb and the words that say what is happening.
 * Size 20 sits inside a line of text or a button; size 64 stands alone,
 * centred, for a page or panel that has nothing else to show yet. */
export function Busy({label,state='searching',size=20}:{label:string;state?:OrbState;size?:20|64}){
  const host=useRef<HTMLSpanElement>(null);
  const [theme,setTheme]=useState<Theme|null>(null);
  useLayoutEffect(()=>{
    const element=host.current;
    if(!element)return;
    const read=()=>setTheme(themeFor(element));
    read();
    // The palette follows the system theme, so the surface is read again when it flips.
    const scheme=matchMedia('(prefers-color-scheme: dark)');
    scheme.addEventListener('change',read);
    return()=>scheme.removeEventListener('change',read);
  },[]);
  return <span ref={host} className={size===64?'busy big':'busy'} role="status">
    <ThinkingOrb state={state} size={size} theme={theme??'auto'} aria-hidden="true" role="presentation"/>
    <span className="busy-label">{label}</span>
  </span>;
}
