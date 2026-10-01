import {useCallback,useEffect,useEffectEvent,useState} from 'react';
import type {Asset} from '../api';
import {ProgressBar} from '../ProgressBar';

const key='cull.review.session';
type Session={path:string;ids:number[];current:number;seconds:number;finished:boolean};
function read():Session|null{try{const value=JSON.parse(localStorage.getItem(key)??'null');return value&&typeof value.path==='string'&&/^\/(on\/\d\d-\d\d|day\/\d{4}-\d\d-\d\d)$/.test(value.path)&&Array.isArray(value.ids)&&value.ids.length<=25&&value.ids.every((id:unknown)=>Number.isInteger(id))&&value.ids.length>0&&value.ids.every((id:number)=>id>0)&&value.ids.includes(value.current)&&Number.isFinite(value.seconds)&&value.seconds>=0&&value.seconds<=300&&typeof value.finished==='boolean'?value:null}catch{return null}}
export function resumeSession(){const value=read();return value&&!value.finished?value.path:null}

export function useReviewSession(assets:Asset[],path:string,viewing:number|null,open:(id:number)=>void,close:()=>void){
  const [session,setSession]=useState<Session|null>(()=>{const value=read();return value?.path===path?value:null});
  const [running,setRunning]=useState(false);
  const selected=session?assets.filter(asset=>session.ids.includes(asset.id)):[];
  const decided=selected.filter(asset=>asset.status==='keep'||asset.status==='cull');
  const finished=!!session&&(session.seconds>=300||selected.length===0||decided.length===selected.length);
  const stop=useEffectEvent(()=>{setRunning(false);close()});
  useEffect(()=>{if(running&&finished)stop()},[running,finished]);
  useEffect(()=>{
    if(!session)return;
    const value={...session,finished};
    try{localStorage.setItem(key,JSON.stringify(value))}catch{/* Review still works without storage. */}
  },[session,finished]);
  useEffect(()=>{
    if(!running||viewing===null)return;
    let last=performance.now();
    const timer=setInterval(()=>{const now=performance.now();const elapsed=Math.min(2,(now-last)/1000);last=now;if(!document.hidden)setSession(value=>value?{...value,seconds:Math.min(300,value.seconds+elapsed)}:value)},1000);
    return()=>clearInterval(timer);
  },[running,viewing]);
  useEffect(()=>{if(viewing===null)setRunning(false)},[viewing]);
  function start(){
    const ids=assets.filter(asset=>asset.status==='unreviewed'||asset.status==='later').slice(0,25).map(asset=>asset.id);
    if(!ids.length)return;
    setSession({path,ids,current:ids[0],seconds:0,finished:false});setRunning(true);open(ids[0]);
  }
  function resume(){if(!session)return;const next=selected.find(asset=>asset.id===session.current&&asset.status==='unreviewed')??selected.find(asset=>asset.status==='unreviewed'||asset.status==='later');if(next){setRunning(true);open(next.id)}}
  const removed=decided.filter(asset=>asset.status==='cull');
  const seconds=Math.max(0,300-Math.floor(session?.seconds??0));
  const ui=<section className="reviewsession" aria-label="Short review session">
    {session&&<><span role="status">{decided.length} of {selected.length} photos reviewed · {Math.floor(seconds/60)}:{String(seconds%60).padStart(2,'0')} left</span><ProgressBar label="Session progress" value={decided.length} max={Math.max(1,selected.length)}/></>}
    {finished&&<span>{decided.length-removed.length} kept · {removed.length} marked for the Bin. No space is freed until permanent deletion.</span>}
    {running?<button className="btn" onClick={()=>{setRunning(false);close()}}>Pause session</button>:session&&!finished?<button className="btn" onClick={resume}>Resume session</button>:<button className="btn" onClick={start} disabled={!assets.some(asset=>asset.status==='unreviewed'||asset.status==='later')}>Review for 5 minutes</button>}
    {!session&&<span className="hint">Up to 25 photos from this day. Pause and resume in this browser.</span>}
  </section>;
  const moved=useCallback((id:number)=>setSession(value=>value&&value.current!==id&&value.ids.includes(id)?{...value,current:id}:value),[]);
  const control=running?<button className="sessionpause" onClick={()=>{setRunning(false);close()}} aria-label="Pause review session">{decided.length}/{selected.length} reviewed · {Math.floor(seconds/60)}:{String(seconds%60).padStart(2,'0')} · Pause</button>:null;
  return {ui,control,moved,ids:running?session?.ids:null};
}
