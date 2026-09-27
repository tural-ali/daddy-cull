import {useEffect,useMemo,useState} from 'react';
import {binChanged,type Asset,type Status} from '../api';
import {Media} from '../Media';
import {useDecisionQueue} from '../useDecisionQueue';
import {calendarLabel} from './Year';
import {Viewer} from './Viewer';
import {usePhotoURL} from './photoURL';
import {Busy} from '../Busy';
import {usePageActions} from './pageActions';
import {historyKey,undoKeys,useHistory,type HistoryEntry,type Snapshot} from './history';

export type TodayYear={day:string;year:number;files:number;bytes:number;status:'pending'|'done';assets:Asset[]};
export type TodayData={md:string;label:string;previous:string;next:string;years:TodayYear[];memories:number;bytes:number};
export type DuplicateMember=Asset&{day:string};
export type DuplicateGroup={hash:string;size:number;reclaimable:number;members:DuplicateMember[]};

function requestID(){return Array.from(crypto.getRandomValues(new Uint8Array(16)),byte=>byte.toString(16).padStart(2,'0')).join('')}
function currentMD(){const now=new Date();return `${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`}
function bytes(value:number){
  if(value<1024)return `${value} B`;
  if(value<1024**2)return `${(value/1024).toFixed(1)} KB`;
  if(value<1024**3)return `${(value/1024**2).toFixed(1)} MB`;
  return `${(value/1024**3).toFixed(1)} GB`;
}
function captureTime(timestamp:number){return timestamp?new Date(timestamp*1000).toLocaleTimeString([],{hour:'2-digit',minute:'2-digit'}):null}

function snapshot(asset:Asset):Snapshot{return {id:asset.id,status:asset.status,favourite:asset.favourite}}
function fileName(asset:Asset){return asset.path.split('/').pop()??''}
/** What a single decision did, for the undo message. */
function describe(asset:Asset,status:Status,favourite:boolean){
  const name=fileName(asset);
  if(status==='cull')return `removed ${name}`;
  if(favourite!==asset.favourite)return favourite?`favourited ${name}`:`unfavourited ${name}`;
  if(status==='keep')return `kept ${name}`;
  if(asset.status==='cull')return `brought back ${name}`;
  return `cleared ${name}`;
}

async function setProgress(day:string,status:'pending'|'done'){
  const response=await fetch('/api/day-progress',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({day,status,requestId:requestID()})});
  if(!response.ok)throw new Error('The review marker could not be saved.');
}

/** How long the keyboard tip stays, and whether this page load has had it. */
const tipTime=15000;
/** How long a notice in the corner stays. */
const snackTime=6000;
let tipShown=false;
const tipKey='cull.tip';

/** Whether the tip is due: on the first day opened in this tab, and again
 * after a refresh, but not while the reviewer moves from day to day. Every
 * day link is a page load, so the load's own kind tells the two apart. */
function tipDue():boolean{
  try{
    const load=performance.getEntriesByType('navigation')[0] as PerformanceNavigationTiming|undefined;
    if(load?.type==='reload')return true;
    return sessionStorage.getItem(tipKey)===null;
  }catch{return true}
}

export function Today({initial}:{initial:TodayData}){
  const [years,setYears]=useState(initial.years);
  const [selected,setSelected]=useState<number|null>(null);
  // The keyboard tip comes up as a snackbar in the corner and goes on its
  // own; moving between days does not bring it back, a refresh does.
  const [tip,setTip]=useState(false);
  useEffect(()=>{
    if(tipShown)return;
    tipShown=true;
    if(!tipDue())return;
    try{sessionStorage.setItem(tipKey,'shown')}catch{/* storage blocked: the tip shows on every day */}
    setTip(true);
  },[]);
  useEffect(()=>{if(!tip)return;const timer=setTimeout(()=>setTip(false),tipTime);return()=>clearTimeout(timer)},[tip]);
  const [saving,setSaving]=useState(false);
  // A notice in the corner, gone on its own unless a save is still running.
  const [message,setMessage]=useState('');
  useEffect(()=>{if(!message||saving)return;const timer=setTimeout(()=>setMessage(''),snackTime);return()=>clearTimeout(timer)},[message,saving]);
  const [duplicateGroups,setDuplicateGroups]=useState<DuplicateGroup[]>([]);
  const [keepers,setKeepers]=useState<Record<string,number>>({});
  const history=useHistory();
  const photo=usePhotoURL(id=>initial.years.some(year=>year.assets.some(asset=>String(asset.id)===id)));
  const viewer=photo.open===null?null:Number(photo.open);
  const assets=useMemo(()=>years.flatMap(year=>year.assets),[years]);
  // A photograph counts as reviewed once it has a decision or its year on this
  // date is marked reviewed, so marking a date fills the bar.
  const reviewed=years.reduce((sum,year)=>sum+(year.status==='done'?year.assets.length:year.assets.filter(asset=>asset.status!=='unreviewed').length),0);
  const doneYears=years.filter(year=>year.status==='done').length;
  const dateDone=years.length>0&&doneYears===years.length;

  function patchAsset(id:number,change:Partial<Asset>){
    setYears(current=>current.map(year=>({...year,assets:year.assets.map(asset=>asset.id===id?{...asset,...change}:asset)})));
    setDuplicateGroups(current=>current.map(group=>({...group,members:group.members.map(asset=>asset.id===id?{...asset,...change}:asset)})));
  }
  async function resolveGroup(group:DuplicateGroup){
    if(saving)return;
    const keeperID=keepers[group.hash]??group.members[0].id;
    const changes=group.members.map(asset=>({assetId:asset.id,status:asset.id===keeperID?'keep':'cull',favourite:asset.id===keeperID&&asset.favourite,expectedRevision:asset.revision,requestId:requestID()}));
    setSaving(true);
    setMessage('Saving the duplicate choices…');
    try{
      const response=await fetch('/api/decisions/batch',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(changes)});
      if(!response.ok)throw new Error(response.status===409?'One of these files changed. Reload before resolving this group.':'The duplicate choices could not be confirmed.');
      const results:{revision:number}[]=await response.json();
      binChanged();
      group.members.forEach((asset,index)=>patchAsset(asset.id,{status:asset.id===keeperID?'keep':'cull',favourite:asset.id===keeperID&&asset.favourite,revision:results[index].revision}));
      history.record({kind:'decisions',label:`resolved ${group.members.length} copies`,before:group.members.map(snapshot),after:changes.map(change=>({id:change.assetId,status:change.status as Status,favourite:change.favourite}))});
	  setDuplicateGroups(current=>current.filter(item=>item.hash!==group.hash||item.size!==group.size));
      setMessage(`${group.members.length-1} verified ${group.members.length===2?'copy':'copies'} marked for the Bin. No original has moved.`);
    }catch(error){setMessage((error as Error).message)}finally{setSaving(false)}
  }
  const queue=useDecisionQueue((job,result)=>{
    patchAsset(job.asset.id,{status:job.status,favourite:job.favourite,revision:result.revision});
  });
  function save(asset:Asset,status:Status,favourite=asset.favourite,remember=true){
    if(status==='cull')favourite=false;
    const before={status:asset.status,favourite:asset.favourite};
    patchAsset(asset.id,{status,favourite});
    if(!queue.enqueue({asset,status,favourite,wasResolved:asset.status==='keep'||asset.status==='cull'})){
      patchAsset(asset.id,before);
      setMessage(queue.error||'Review is paused until the pending choice is confirmed.');
      return false;
    }
    if(remember){
      history.record({kind:'decisions',label:describe(asset,status,favourite),before:[snapshot(asset)],after:[{id:asset.id,status,favourite}]});
    }
    return true;
  }
  // Undo puts every file of the last action back as it was, through the same
  // queue as any choice, so it is journaled and confirmed the same way. A file
  // already back in that state (undone by hand) is left alone.
  function restore(entry:HistoryEntry,direction:'before'|'after'){
    if(entry.kind==='progress'){
      const status=entry[direction];
      setSaving(true);
      Promise.all(entry.days.map(day=>setProgress(day,status))).then(()=>{
        setYears(current=>current.map(year=>entry.days.includes(year.day)?{...year,status}:year));
        binChanged();
      }).catch(error=>setMessage((error as Error).message)).finally(()=>setSaving(false));
      return true;
    }
    for(const target of entry[direction]){
      const asset=assets.find(item=>item.id===target.id);
      if(!asset||(asset.status===target.status&&asset.favourite===target.favourite))continue;
      if(!save(asset,target.status,target.favourite,false))return false;
    }
    return true;
  }
  function undo(){
    const taken=history.takeUndo();
    if(!taken){setMessage('Nothing to undo.');return}
    if(restore(taken.entry,'before'))setMessage(`Undone: ${taken.entry.label}.`);else taken.keep();
  }
  function redo(){
    const taken=history.takeRedo();
    if(!taken){setMessage('Nothing to redo.');return}
    if(restore(taken.entry,'after'))setMessage(`Redone: ${taken.entry.label}.`);else taken.keep();
  }
  async function markDate(){
    if(saving)return;
    setSaving(true);
    setMessage('Saving…');
    try{
      const open=years.filter(year=>year.status!=='done').map(year=>year.day);
      await Promise.all(open.map(day=>setProgress(day,'done')));
      setYears(current=>current.map(year=>({...year,status:'done'})));
      history.record({kind:'progress',label:`marked ${initial.label} reviewed`,days:open,before:'pending',after:'done'});
      binChanged();
      setMessage(`${initial.label} marked reviewed.`);
    }catch(error){setMessage((error as Error).message)}finally{setSaving(false)}
  }

  // Undo and redo work wherever the day is being reviewed, in the grid or in
  // the viewer, which leaves modifier keys alone.
  useEffect(()=>{
    function key(event:KeyboardEvent){
      const action=historyKey(event);
      if(!action)return;
      event.preventDefault();
      if(event.repeat||saving)return;
      if(action==='undo')undo();else redo();
    }
    window.addEventListener('keydown',key);
    return()=>window.removeEventListener('keydown',key);
  });
  useEffect(()=>{
    function key(event:KeyboardEvent){
      if(viewer!==null||event.ctrlKey||event.metaKey||event.altKey||event.target instanceof HTMLButtonElement)return;
      const index=assets.findIndex(asset=>asset.id===selected);
      if(event.key==='ArrowRight'){
        event.preventDefault();
        setSelected(assets[Math.min(assets.length-1,Math.max(0,index+1))]?.id??null);
      }else if(event.key==='ArrowLeft'){
        event.preventDefault();
        setSelected(assets[Math.max(0,index-1)]?.id??null);
      }else if(!event.repeat&&selected!==null){
        const asset=assets.find(item=>item.id===selected);
        if(!asset)return;
        if(event.key.toLowerCase()==='x')save(asset,asset.status==='cull'?'unreviewed':'cull');
        if(event.key.toLowerCase()==='f')save(asset,asset.status==='cull'?'unreviewed':asset.status,!asset.favourite);
      }
    }
    window.addEventListener('keydown',key);
    return()=>window.removeEventListener('keydown',key);
  },[assets,selected,saving]);

  useEffect(()=>{
    const controller=new AbortController();
    fetch(`/api/duplicates?md=${initial.md}`,{signal:controller.signal}).then(async response=>{
      if(!response.ok)throw new Error('Could not load verified duplicates.');
      const groups:DuplicateGroup[]=await response.json();
      setDuplicateGroups(groups);
      setKeepers(Object.fromEntries(groups.map(group=>[group.hash,group.members[0].id])));
    }).catch(error=>{if(!controller.signal.aborted)setMessage((error as Error).message)});
    return()=>controller.abort();
  },[initial.md]);

  usePageActions(assets.length>0&&doneYears<years.length?{actions:[{label:`Mark ${initial.label} reviewed`,short:'Mark reviewed',icon:'task_alt',disabled:saving,onClick:()=>void markDate()}]}:null);
  return <>
    <div className="dhead">
      <a className="step" data-nav="prev" href={`/on/${initial.previous}`}>← {calendarLabel(initial.previous)}</a>
      <h1>{initial.label}{initial.md===currentMD()&&<span className="tag today">today</span>}{dateDone&&<span className="tag done">reviewed</span>}</h1>
      <a className="step" data-nav="next" href={`/on/${initial.next}`}>{calendarLabel(initial.next)} →</a>
    </div>
    <div className="dline">
      <span className="count">{initial.memories.toLocaleString()} {initial.memories===1?'memory':'memories'}</span>
      <span className="sep">·</span><span>{years.length} {years.length===1?'year':'years'}</span>
      <span className="sep">·</span><span className="dim">{bytes(initial.bytes)}</span>
    </div>
    {assets.length>0&&<div className="dprog"><div className="pbar" role="progressbar" aria-label="Memories reviewed on this date" aria-valuemin={0} aria-valuemax={assets.length} aria-valuenow={reviewed}><span style={{width:`${reviewed/assets.length*100}%`}}/></div><span className="ofn">{reviewed.toLocaleString()} of {assets.length.toLocaleString()} reviewed</span></div>}
    {queue.error&&<p className="note warn" role="alert">{queue.error} <button className="btn small" onClick={queue.retry}>Retry the same save</button></p>}
    {years.length===0&&<p className="note">Nothing in the archive is filed under {initial.label}, so there is nothing to review.</p>}
    {duplicateGroups.length>0&&<section className="xdupes">
      <h2>Same file, different folders <small>{duplicateGroups.length} {duplicateGroups.length===1?'group':'groups'} · byte-identical, verified by full hash</small></h2>
      {duplicateGroups.map(group=><div className="xgroup" key={`${group.hash}:${group.size}`}>
        <p className="xmeta">{group.members.length} identical copies · {bytes(group.size)} each · <strong>{bytes(group.reclaimable)}</strong> reclaimable</p>
        <div className="gal tight">{group.members.map(member=><figure className={`mo${(keepers[group.hash]??group.members[0].id)===member.id?' keeper':''}`} key={member.id}>
          <Media asset={member}/>
          <div className="bdg"><button type="button" className="b tocmp" disabled={saving} onClick={()=>setKeepers(current=>({...current,[group.hash]:member.id}))}>{(keepers[group.hash]??group.members[0].id)===member.id?'keep this one':'choose as keeper'}</button></div>
          <figcaption className="cap"><span>{member.day}</span></figcaption>
        </figure>)}</div>
        <p className="xact"><button type="button" className="btn small danger" disabled={saving} onClick={()=>void resolveGroup(group)}>Keep the selected copy, mark the other {group.members.length-1} for the Bin</button><span className="hint">Nothing is deleted. The Bin remains separately reviewable and restorable.</span></p>
      </div>)}
    </section>}
    {years.map(year=><section className={`yr${year.status==='done'?' settled':''}`} key={year.day}>
      <div className="yhead"><h2>{year.year}{year.status==='done'&&<span className="tag done">reviewed</span>}</h2><p className="ymeta"><span>{year.assets.length.toLocaleString()} {year.assets.length===1?'memory':'memories'}{year.assets.length!==year.files&&<span className="dim"> from {year.files.toLocaleString()} files</span>}</span><span className="dim">{bytes(year.bytes)}</span></p></div>
      {year.assets.length===0?<p className="note">Nothing left in this folder.</p>:<div className="gal">{year.assets.map(asset=><figure className={`mo${asset.favourite?' fav':''}${asset.status!=='unreviewed'?' seen':''}${asset.status==='cull'?' culled':''}${selected===asset.id?' sel':''}`} key={asset.id} data-asset={asset.id} onClick={()=>{setSelected(asset.id);photo.show(asset.id)}} tabIndex={0}>
        <Media asset={asset}/>
        <div className="bdg">{(asset.relatedCount??0)>0&&<span className="b dupe">duplicate</span>}{asset.kind==='video'&&<span className="b play">▶</span>}</div>
        <div className="acts"><button type="button" className="act cull" disabled={!queue.ready} onClick={event=>{event.stopPropagation();save(asset,asset.status==='cull'?'unreviewed':'cull')}}>{asset.status==='cull'?'Undo':'Remove'}</button><button type="button" className="act fav" disabled={!queue.ready} aria-pressed={asset.favourite} onClick={event=>{event.stopPropagation();save(asset,asset.status==='cull'?'unreviewed':asset.status,!asset.favourite)}}>♡</button></div>
        {captureTime(asset.capturedAt)&&<div className="when">{captureTime(asset.capturedAt)}</div>}
        {asset.status==='cull'&&<div className="undo"><span>Removed</span><button type="button" className="act" disabled={!queue.ready} onClick={event=>{event.stopPropagation();save(asset,'unreviewed')}}>Undo</button></div>}
      </figure>)}</div>}
    </section>)}
    {(message||(tip&&assets.length>0))&&<div className="snacks">
      {message&&<div className="snack" role="status">{saving?<Busy label={message} state="working"/>:message}</div>}
      {tip&&assets.length>0&&<div className="snack" role="status">Click any photo to review. <b>→</b> next, <b>k</b> keep, <b>x</b> remove, <b>f</b> favourite, <b>{undoKeys.undo}</b> undo, <b>{undoKeys.redo}</b> redo, <b>?</b> for the rest</div>}
    </div>}
    {viewer!==null&&<Viewer assets={assets} initialID={viewer} onClose={photo.close} onMove={photo.moved} onSave={save} onPatch={patchAsset} onRecord={history.record}/>} 
  </>;
}
