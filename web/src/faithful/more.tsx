import {useEffect,useEffectEvent,useRef,useState} from 'react';
import {Busy} from '../Busy';

// A long list loads as it is scrolled, the way Google Photos keeps a library
// in one scroll: a marker after the last tile asks for the next page once it
// comes within a screen and a half of view, and asks again after each page
// lands for as long as it is still that close.

/** `more` says whether the server has more; `count` is how many are loaded,
 * so a page that lands, or a decision that shortens the list, is looked at
 * again. `list` names what is listed, the filters: a new list is measured
 * afresh, since the marker was near the end of the old one. A failed read
 * waits for Try again rather than asking in a loop. */
export function useMoreOnScroll(list:string,more:boolean,count:number,load:()=>Promise<void>){
  const marker=useRef<HTMLDivElement>(null);
  // Which list the marker was last seen near the end of, if any.
  const [near,setNear]=useState<string|null>(null);
  const [loading,setLoading]=useState(false);
  const [failed,setFailed]=useState(false);
  useEffect(()=>{
    const node=marker.current;
    if(!node)return;
    const observer=new IntersectionObserver(([entry])=>setNear(entry.isIntersecting?list:null),{rootMargin:'0px 0px 150% 0px'});
    observer.observe(node);
    return()=>observer.disconnect();
  },[list,more]);
  const next=useEffectEvent(async()=>{
    setLoading(true);
    try{await load()}catch{setFailed(true)}finally{setLoading(false)}
  });
  useEffect(()=>{if(near===list&&more&&!loading&&!failed)void next()},[near,list,more,loading,failed,count]);
  return {marker,loading,failed,retry:()=>setFailed(false)};
}

/** The marker itself, which shows the page loading, or why it stopped. */
export function MoreMarker({state,what}:{state:ReturnType<typeof useMoreOnScroll>;what:string}){
  return <div ref={state.marker} className="more" aria-live="polite">
    {state.failed
      ?<p className="note warn">More {what} could not be loaded. <button type="button" className="btn small" onClick={state.retry}>Try again</button></p>
      :state.loading&&<Busy label={`Loading more ${what}…`}/>}
  </div>;
}
