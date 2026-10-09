// Touch on the review stage, answered the way the Photos app answers it: the
// photo follows a finger sideways while its neighbour slides in behind it, a
// pull down sends it back to its tile, a push up opens Info, and two taps or a
// pinch zoom in where the fingers are. A mouse and the keys keep their own
// ways, so only touch reaches any of this.
import {useRef,useState,type PointerEvent,type MouseEvent,type RefObject} from 'react';
import {flushSync} from 'react-dom';

type Point={x:number;y:number};
type Track={start:Point;at:Point;time:number;axis:'x'|'y'|null;samples:{x:number;y:number;t:number}[]};

/** Room between a photo and the next while both are on the stage. */
export const peekGap=24;
const lock=10;          // pixels a finger travels before the gesture is chosen
const settleTime=220;
const doubleTap=280;

export type StageGestures={
  /** -1 or 1 while the previous or next photo is drawn beside this one. */
  peek:-1|1|0;
  handlers:{
    onPointerDown:(event:PointerEvent)=>void;
    onPointerMove:(event:PointerEvent)=>void;
    onPointerUp:(event:PointerEvent)=>void;
    onPointerCancel:(event:PointerEvent)=>void;
  };
  /** True while a click is the end of a touch, so it is a tap. */
  touched:()=>boolean;
  /** A tap on the stage: one hides the controls after a moment, a second on
   * the photo zooms to where it landed instead. */
  tap:(event:MouseEvent,onPhoto:boolean)=>void;
  /** True for a click that ends a swipe or a pull, which is not a tap. */
  swallowed:()=>boolean;
};

/** `canStep` is false for a review of one photo, which gives a little and
 * springs back rather than turning to itself. */
export function useStageGestures({stage,root,enabled,canStep,infoOpen,onStep,onLeave,onInfo,onBare,onZoomAt}:{
  stage:RefObject<HTMLDivElement|null>;
  root:RefObject<HTMLDivElement|null>;
  enabled:boolean;
  canStep:boolean;
  infoOpen:boolean;
  onStep:(change:number)=>void;
  onLeave:()=>void;
  onInfo:(open:boolean)=>void;
  onBare:()=>void;
  onZoomAt:(point:Point)=>void;
}):StageGestures{
  const [peek,setPeek]=useState<-1|1|0>(0);
  const fingers=useRef(new Map<number,Point>());
  const track=useRef<Track|null>(null);
  const pinch=useRef<{span:number;done:boolean}|null>(null);
  const lastTouch=useRef(0);
  const gestureEnded=useRef(0);
  const settling=useRef(false);
  const pendingTap=useRef<{timer:number;at:number}|null>(null);

  const set=(name:string,value:string|null)=>{
    const element=stage.current;
    if(!element)return;
    if(value===null)element.style.removeProperty(name);else element.style.setProperty(name,value);
  };
  // Attributes rather than classes: React rewrites the class of either
  // element whenever its own part of it changes, as it does when Back starts.
  const mode=(name:'dragging'|'settling'|'pulling',on:boolean)=>{
    stage.current?.toggleAttribute(`data-${name}`,on);
    if(name!=='dragging')root.current?.toggleAttribute(`data-${name}`,on);
  };
  /** Moves the drag to `to` with the settling ease, then calls `done`. */
  function settle(property:string,to:string,done:()=>void){
    const element=stage.current;
    if(!element){done();return}
    settling.current=true;
    mode('dragging',false);mode('settling',true);
    set(property,to);
    let finished=false;
    const finish=()=>{if(finished)return;finished=true;settling.current=false;mode('settling',false);done()};
    window.setTimeout(finish,settleTime+40);
  }
  function reset(){
    set('--drag',null);set('--pull',null);root.current?.style.removeProperty('--pulled');
    mode('dragging',false);mode('pulling',false);
    setPeek(0);
  }
  function velocity(samples:Track['samples'],axis:'x'|'y'){
    const last=samples[samples.length-1];
    const first=samples.find(sample=>last.t-sample.t<=100)??samples[0];
    const elapsed=last.t-first.t;
    return elapsed>0?(last[axis]-first[axis])/elapsed:0;
  }

  function onPointerDown(event:PointerEvent){
    if(event.pointerType!=='touch')return;
    lastTouch.current=performance.now();
    if(!enabled||settling.current)return;
    fingers.current.set(event.pointerId,{x:event.clientX,y:event.clientY});
    if(fingers.current.size===2){
      // A second finger turns whatever the first began into a pinch.
      const [a,b]=[...fingers.current.values()];
      if(track.current?.axis)reset();
      track.current=null;
      pinch.current={span:Math.hypot(a.x-b.x,a.y-b.y),done:false};
      return;
    }
    if(fingers.current.size>2)return;
    (event.currentTarget as HTMLElement).setPointerCapture?.(event.pointerId);
    const point={x:event.clientX,y:event.clientY};
    track.current={start:point,at:point,time:event.timeStamp,axis:null,samples:[{...point,t:event.timeStamp}]};
  }

  function onPointerMove(event:PointerEvent){
    if(event.pointerType!=='touch'||!fingers.current.has(event.pointerId))return;
    fingers.current.set(event.pointerId,{x:event.clientX,y:event.clientY});
    if(pinch.current){
      if(pinch.current.done||fingers.current.size<2)return;
      const [a,b]=[...fingers.current.values()];
      if(Math.hypot(a.x-b.x,a.y-b.y)/pinch.current.span>1.3){
        pinch.current.done=true;
        onZoomAt({x:(a.x+b.x)/2,y:(a.y+b.y)/2});
      }
      return;
    }
    const drag=track.current;
    if(!drag)return;
    drag.at={x:event.clientX,y:event.clientY};
    drag.samples.push({...drag.at,t:event.timeStamp});
    if(drag.samples.length>12)drag.samples.shift();
    const dx=drag.at.x-drag.start.x,dy=drag.at.y-drag.start.y;
    if(!drag.axis){
      if(Math.max(Math.abs(dx),Math.abs(dy))<lock)return;
      drag.axis=Math.abs(dx)>Math.abs(dy)?'x':'y';
      if(drag.axis==='x'){mode('dragging',true);if(canStep)setPeek(dx<0?1:-1)}
      else if(dy>0&&!infoOpen)mode('pulling',true);
    }
    if(drag.axis==='x'){
      const side=dx<0?1:-1;
      if(canStep&&side!==peek)setPeek(side);
      set('--drag',`${canStep?dx:dx*.3}px`);
    }else if(dy>0&&!infoOpen){
      const height=stage.current?.clientHeight||1;
      set('--pull',`${dy}px`);
      root.current?.style.setProperty('--pulled',String(Math.min(1,dy/(height*.6))));
    }
  }

  function onPointerUp(event:PointerEvent){
    if(event.pointerType!=='touch')return;
    fingers.current.delete(event.pointerId);
    if(pinch.current){
      if(fingers.current.size===0){if(pinch.current.done)gestureEnded.current=performance.now();pinch.current=null}
      return;
    }
    const drag=track.current;
    track.current=null;
    if(!drag?.axis)return;
    gestureEnded.current=performance.now();
    const dx=drag.at.x-drag.start.x,dy=drag.at.y-drag.start.y;
    const width=stage.current?.clientWidth||1;
    if(drag.axis==='x'){
      const speed=velocity(drag.samples,'x');
      const side=dx<0?1:-1;
      const far=canStep&&(Math.abs(dx)>width*.18||Math.abs(speed)>.35&&Math.sign(speed)===-side);
      if(far)settle('--drag',`${-side*(width+peekGap)}px`,()=>{flushSync(()=>onStep(side));reset()});
      else settle('--drag','0px',reset);
      return;
    }
    const speed=velocity(drag.samples,'y');
    if(dy<-40||speed<-.4&&dy<0){if(!infoOpen)onInfo(true);reset();return}
    if(infoOpen){if(dy>40||speed>.4)onInfo(false);reset();return}
    // Pulled far enough or fast enough down, the photo flies back to its tile
    // from where the finger left it.
    if(dy>120||speed>.5&&dy>30){mode('dragging',false);onLeave();return}
    settle('--pull','0px',reset);
    root.current?.style.setProperty('--pulled','0');
  }

  function onPointerCancel(event:PointerEvent){
    if(event.pointerType!=='touch')return;
    fingers.current.delete(event.pointerId);
    if(fingers.current.size===0)pinch.current=null;
    if(track.current?.axis)settle(track.current.axis==='x'?'--drag':'--pull','0px',reset);
    track.current=null;
  }

  function tap(event:MouseEvent,onPhoto:boolean){
    const pending=pendingTap.current;
    if(pending&&performance.now()-pending.at<doubleTap){
      window.clearTimeout(pending.timer);
      pendingTap.current=null;
      if(onPhoto)onZoomAt({x:event.clientX,y:event.clientY});
      return;
    }
    const timer=window.setTimeout(()=>{pendingTap.current=null;onBare()},doubleTap);
    pendingTap.current={timer,at:performance.now()};
  }

  return {
    peek,
    handlers:{onPointerDown,onPointerMove,onPointerUp,onPointerCancel},
    touched:()=>performance.now()-lastTouch.current<1000,
    tap,
    swallowed:()=>performance.now()-gestureEnded.current<400,
  };
}
