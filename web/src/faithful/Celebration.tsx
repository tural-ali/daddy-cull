import {useEffect,useRef,useState} from 'react';

export type Tally={label:string;total:number;removed:number;bytes:number;favourites:number};

const colours=['#f397a2','#f7c66a','#7ed9b2','#ffffff'];
const countTime=1100;
const confettiTime=3200;

function easeOut(t:number){return 1-(1-t)**3}

/** A number that climbs from nought to its value as the card settles. */
function useCountUp(value:number,run:boolean){
  const [shown,setShown]=useState(run?0:value);
  useEffect(()=>{
    if(!run){setShown(value);return}
    let frame=0;
    const start=performance.now();
    const tick=(now:number)=>{
      const t=Math.min(1,(now-start)/countTime);
      setShown(Math.round(value*easeOut(t)));
      if(t<1)frame=requestAnimationFrame(tick);
    };
    frame=requestAnimationFrame(tick);
    return()=>cancelAnimationFrame(frame);
  },[value,run]);
  return shown;
}

function stillMotion(){return matchMedia('(prefers-reduced-motion: reduce)').matches}

/** Paper falling over the page: a burst from the top centre in the logo's
 * colours, drawn on a canvas for as long as it takes to settle. */
function Confetti(){
  const canvas=useRef<HTMLCanvasElement>(null);
  useEffect(()=>{
    const element=canvas.current;
    const context=element?.getContext('2d');
    if(!element||!context)return;
    const scale=Math.min(2,devicePixelRatio||1);
    const fit=()=>{element.width=innerWidth*scale;element.height=innerHeight*scale};
    fit();
    addEventListener('resize',fit);
    const pieces=Array.from({length:180},(_,i)=>{
      const angle=-Math.PI/2+(Math.random()-.5)*Math.PI*.9;
      const speed=9+Math.random()*9;
      return {
        x:innerWidth/2+(Math.random()-.5)*innerWidth*.25,y:innerHeight*.28,
        vx:Math.cos(angle)*speed,vy:Math.sin(angle)*speed,
        w:6+Math.random()*6,h:8+Math.random()*8,
        spin:(Math.random()-.5)*.3,angle:Math.random()*Math.PI,
        tilt:Math.random()*Math.PI,tiltSpeed:.1+Math.random()*.2,
        colour:colours[i%colours.length],delay:Math.random()*220,
      };
    });
    let frame=0;
    const start=performance.now();
    let last=start;
    const draw=(now:number)=>{
      const step=Math.min(2,(now-last)/16.7);
      last=now;
      context.setTransform(scale,0,0,scale,0,0);
      context.clearRect(0,0,innerWidth,innerHeight);
      let alive=false;
      for(const piece of pieces){
        if(now-start<piece.delay)continue;
        piece.vy+=.32*step;
        piece.vx*=.985;
        piece.vy*=.985;
        piece.x+=piece.vx*step;
        piece.y+=piece.vy*step;
        piece.angle+=piece.spin*step;
        piece.tilt+=piece.tiltSpeed*step;
        if(piece.y>innerHeight+20)continue;
        alive=true;
        const fade=Math.max(0,Math.min(1,(start+confettiTime-now)/600));
        context.globalAlpha=fade;
        context.fillStyle=piece.colour;
        context.translate(piece.x,piece.y);
        context.rotate(piece.angle);
        context.scale(1,Math.max(.2,Math.abs(Math.cos(piece.tilt))));
        context.fillRect(-piece.w/2,-piece.h/2,piece.w,piece.h);
        context.setTransform(scale,0,0,scale,0,0);
      }
      if(alive&&now-start<confettiTime)frame=requestAnimationFrame(draw);
      else context.clearRect(0,0,innerWidth,innerHeight);
    };
    frame=requestAnimationFrame(draw);
    return()=>{cancelAnimationFrame(frame);removeEventListener('resize',fit)};
  },[]);
  return <canvas ref={canvas} className="confetti" aria-hidden="true"/>;
}

/** The card that comes up over a blurred day once it is marked reviewed:
 * what was cleared, how much room that made and what was kept close. */
export function Celebration({tally,nextHref,nextLabel,onClose}:{tally:Tally;nextHref:string;nextLabel:string;onClose:()=>void}){
  const animate=!stillMotion();
  const removed=useCountUp(tally.removed,animate);
  const favourites=useCountUp(tally.favourites,animate);
  const freed=useCountUp(tally.bytes,animate);
  useEffect(()=>{
    const key=(event:KeyboardEvent)=>{if(event.key==='Escape'){event.preventDefault();onClose()}};
    addEventListener('keydown',key);
    return()=>removeEventListener('keydown',key);
  },[onClose]);
  const size=freed<1024**2?`${(freed/1024).toFixed(0)} KB`:freed<1024**3?`${(freed/1024**2).toFixed(1)} MB`:`${(freed/1024**3).toFixed(2)} GB`;
  return <div className="cheer" role="dialog" aria-modal="true" aria-label={`${tally.label} reviewed`} onClick={onClose}>
    {animate&&<Confetti/>}
    <div className="cheercard" onClick={event=>event.stopPropagation()}>
      <p className="eyebrow">Day reviewed</p>
      <h2>{tally.label}</h2>
      <dl>
        <div><dt>Freed</dt><dd>{size}</dd></div>
        <div><dt>Cleaned out</dt><dd>{removed.toLocaleString()}<small> of {tally.total.toLocaleString()}</small></dd></div>
        <div><dt>Favourited</dt><dd>{favourites.toLocaleString()}</dd></div>
      </dl>
      <p className="cheeracts">
        <button type="button" className="btn" onClick={onClose}>Stay here</button>
        <a className="btn primary" href={nextHref}>{nextLabel} →</a>
      </p>
    </div>
  </div>;
}
