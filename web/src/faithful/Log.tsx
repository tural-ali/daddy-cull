import {Fragment,useState} from 'react';
import {decide,type Asset} from '../api';
import {Media} from '../Media';

export type HistoryEvent={requestId:string;asset:Asset;status:string;favourite:boolean;previousStatus:'unreviewed'|'keep'|'later'|'cull';previousFavourite:boolean;createdAt:string};
// The server sends every time as UTC; the day an action belongs to is the
// reader's own, so a late-evening choice is not filed under tomorrow.
function localDay(at:Date){return `${at.getFullYear()}-${at.getMonth()}-${at.getDate()}`}
// The earlier tool's heading, "Tuesday 15 September 2026", with no comma.
function dayHeading(at:Date){return `${at.toLocaleDateString('en-GB',{weekday:'long'})} ${at.toLocaleDateString('en-GB',{day:'numeric',month:'long',year:'numeric'})}`}
function requestID(){return Array.from(crypto.getRandomValues(new Uint8Array(16)),byte=>byte.toString(16).padStart(2,'0')).join('')}
function verb(event:HistoryEvent){
  if(event.favourite&&!event.previousFavourite)return ['Favourited','fav'];
  if(event.status==='cull')return ['Marked for Bin','bad'];
  if(event.status==='keep')return ['Kept','ok'];
  if(event.status==='later')return ['Left for later',''];
  return ['Decision cleared',''];
}

export function Log({initial}:{initial:HistoryEvent[]}){
  const [events,setEvents]=useState(initial);
  const [busy,setBusy]=useState(false);
  const [message,setMessage]=useState('');
  async function undo(event:HistoryEvent){
    if(busy)return;
    setBusy(true);
    try{
      const result=await decide(event.asset,event.previousStatus,event.previousFavourite,requestID());
      setEvents(current=>current.map(item=>item.asset.id===event.asset.id?{...item,asset:{...item.asset,status:event.previousStatus,favourite:event.previousFavourite,revision:result.revision}}:item));
      setMessage('The previous choice was restored and recorded as a new event.');
    }catch(error){setMessage((error as Error).message)}finally{setBusy(false)}
  }
  return <>
    <section className="dupehead"><h1>Log</h1><p className="ysum"><b>{events.length.toLocaleString()}</b> latest saved actions</p><p className="note">Every line is a saved choice. Opening a photo and leaving it alone is not recorded.</p>{message&&<p className="flash" role="status">{message}</p>}</section>
    {events.length===0?<p className="note">Nothing recorded yet.</p>:<ol className="log">{events.map((event,index)=>{const [label,tone]=verb(event);const at=new Date(event.createdAt);const day=localDay(at);const opensDay=index===0||localDay(new Date(events[index-1].createdAt))!==day;return <Fragment key={event.requestId}>
      {opensDay&&<li className="lday">{dayHeading(at)}</li>}
      <li className="lrow">
      <span className="lwhen">{at.toLocaleTimeString('en-GB',{hour:'2-digit',minute:'2-digit'})}</span><span className={`lverb ${tone}`}>{label}</span><span className="lthumb"><Media asset={event.asset}/></span><span className="lname"><a href={`/on/${new Date(event.asset.capturedAt*1000).toISOString().slice(5,10)}`}>{event.asset.path.split('/').pop()}</a><span className="lpath">{event.asset.path.slice(0,event.asset.path.lastIndexOf('/'))}</span></span><span className="lsize"><button className="btn small" disabled={busy} onClick={()=>void undo(event)}>Undo</button></span>
    </li></Fragment>})}</ol>}
  </>;
}
