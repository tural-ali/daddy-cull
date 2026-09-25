import {Fragment,useMemo,useState} from 'react';
import {decide,type Asset,type Status} from '../api';
import {Media} from '../Media';
import {Deleting} from './Bin';
import {Viewer} from './Viewer';

export type HistoryEvent={requestId:string;asset:Asset;status:string;favourite:boolean;previousStatus:'unreviewed'|'keep'|'later'|'cull';previousFavourite:boolean;createdAt:string};
// The server sends every time as UTC; the day an action belongs to is the
// reader's own, so a late-evening choice is not filed under tomorrow.
function localDay(at:Date){return `${at.getFullYear()}-${at.getMonth()}-${at.getDate()}`}
// The earlier tool's heading, "Tuesday 15 September 2026", with no comma.
function dayHeading(at:Date){return `${at.toLocaleDateString('en-GB',{weekday:'long'})} ${at.toLocaleDateString('en-GB',{day:'numeric',month:'long',year:'numeric'})}`}
// The day a file belongs to is the folder it is filed in, which is what the day
// page lists; its capture instant in UTC can fall on the day before or after.
function dayOf(asset:Asset){
  const folder=asset.path.match(/\/(\d{4}-\d{2}-\d{2})\//);
  return folder?`/day/${folder[1]}`:`/on/${new Date(asset.capturedAt*1000).toISOString().slice(5,10)}`;
}
function requestID(){return Array.from(crypto.getRandomValues(new Uint8Array(16)),byte=>byte.toString(16).padStart(2,'0')).join('')}
function verb(event:HistoryEvent):[string,string]{
  if(event.favourite&&!event.previousFavourite)return ['Favourited','fav'];
  if(!event.favourite&&event.previousFavourite&&event.status===event.previousStatus)return ['Unfavourited',''];
  if(event.status==='cull')return ['Marked for Bin','bad'];
  if(event.status==='keep')return ['Kept','ok'];
  if(event.status==='later')return ['Left for later',''];
  return ['Decision cleared',''];
}

export function Log({initial}:{initial:HistoryEvent[]}){
  const [events,setEvents]=useState(initial);
  const [undone,setUndone]=useState<Set<string>>(new Set());
  const [busy,setBusy]=useState(false);
  const [message,setMessage]=useState('');
  const [viewing,setViewing]=useState<number|null>(null);
  // The viewer steps through photographs, not events: a file chosen twice is
  // one frame, at the place of its latest choice.
  const assets=useMemo(()=>{
    const byID=new Map<number,Asset>();
    for(const event of events)if(!byID.has(event.asset.id))byID.set(event.asset.id,event.asset);
    return [...byID.values()];
  },[events]);
  function patch(id:number,change:Partial<Asset>){
    setEvents(current=>current.map(item=>item.asset.id===id?{...item,asset:{...item.asset,...change}}:item));
  }
  async function undo(event:HistoryEvent){
    if(busy)return;
    setBusy(true);
    try{
      const result=await decide(event.asset,event.previousStatus,event.previousFavourite,requestID());
      patch(event.asset.id,{status:event.previousStatus,favourite:event.previousFavourite,revision:result.revision});
      setUndone(current=>new Set(current).add(event.requestId));
      setMessage(`${event.asset.path.split('/').pop()} is back to how it was. The undo is saved as a new choice.`);
    }catch(error){setMessage((error as Error).message)}finally{setBusy(false)}
  }
  // A choice made in the viewer is saved like any other; the grid shows it once
  // the page is next opened, since the Log lists what was saved, in order.
  function decideInViewer(asset:Asset,status:Status,favourite?:boolean){
    const nextFavourite=favourite??asset.favourite;
    decide(asset,status,nextFavourite,requestID())
      .then(result=>patch(asset.id,{status,favourite:nextFavourite,revision:result.revision}))
      .catch(error=>setMessage((error as Error).message));
    return true;
  }
  return <>
    <section className="dupehead"><h1>Log</h1><p className="ysum"><b>{events.length.toLocaleString()}</b> latest saved choices</p><p className="hint">Every tile is a saved choice. Opening a photo and leaving it alone is not recorded. Click a tile to look at it again.</p>{message&&<p className="flash" role="status">{message}</p>}</section>
    <Deleting/>
    {events.length>0&&<h2 className="logtitle">Saved choices</h2>}
    {events.length===0?<p className="note">Nothing recorded yet.</p>:<div className="loggrid">{events.map((event,index)=>{
      const [label,tone]=verb(event);
      const at=new Date(event.createdAt);
      const opensDay=index===0||localDay(new Date(events[index-1].createdAt))!==localDay(at);
      const isUndone=undone.has(event.requestId);
      const name=event.asset.path.split('/').pop();
      return <Fragment key={event.requestId}>
        {opensDay&&<h3 className="lday">{dayHeading(at)}</h3>}
        <figure className={`mo logtile${isUndone?' undone':''}${event.asset.favourite?' fav':''}`}>
          <button type="button" className="shot" aria-label={`Look at ${name}`} onClick={()=>setViewing(event.asset.id)}><Media asset={event.asset}/></button>
          <div className="bdg"><span className={`b verb ${isUndone?'':tone}`}>{isUndone?'Undone':label}</span></div>
          {!isUndone&&<div className="acts"><button type="button" className="act" disabled={busy} onClick={()=>void undo(event)}>Undo</button></div>}
          <figcaption className="cap"><a href={dayOf(event.asset)} title={`Open ${event.asset.path.slice(0,event.asset.path.lastIndexOf('/'))}`}>{name}</a><span className="dim">{at.toLocaleTimeString('en-GB',{hour:'2-digit',minute:'2-digit'})}</span></figcaption>
        </figure>
      </Fragment>;
    })}</div>}
    {viewing!==null&&<Viewer assets={assets} initialID={viewing} onClose={()=>setViewing(null)} onSave={decideInViewer} onPatch={patch}/>}
  </>;
}
