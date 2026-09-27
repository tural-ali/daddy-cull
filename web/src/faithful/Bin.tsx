import {useEffect,useRef,useState} from 'react';
import {FilePreview} from '../Media';
import {binChanged} from '../api';
import {Icon} from '../Icon';
import {Lightbox,type LightboxItem} from './Lightbox';
import {dayOfPath} from './goto';
import {Busy} from '../Busy';
import {Pick,RowTile,Rows,useSelectionBar,usePicks} from './selection';
import {usePhotoURL} from './photoURL';

/** One card in the Bin, whichever tool put the file there. */
export type TrashItem={key:string;group:string;source:'marked'|'bin'|'legacy'|'screenshot';name:string;original:string;kind:string;size:number;sidecars:number;removedAt:string;preview?:string;disk?:string};
/** A file deleted from the Bin and still on disk until its grace period ends. */
export type DeletingItem=TrashItem&{deletedAt:string;dueAt:string;attempts:number;lastError?:string};
export type DeletingReport={graceDays:number;graceError?:string;items:DeletingItem[];lastRun:string;lastDeleted:number;lastError:string;checkIntervalMinutes:number};
type Result={done:number;bytes:number;failures:{name:string;error:string}[];keptDays?:number};
type Pending={title:string;body:string;confirm:string;run:()=>Promise<void>};
type Control='empty'|'restore'|'delete'|'lightbox';
const minimumBusy=600;

async function post(path:string,body:unknown):Promise<Result>{
  const response=await fetch(path,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});
  const text=await response.text();
  let result:unknown;
  try{result=JSON.parse(text)}catch{throw new Error('The Bin could not confirm what happened. Reload the page to see what it holds now.')}
  if(!response.ok)throw new Error((result as {error?:string}).error||'The Bin request failed.');
  return result as Result;
}
export async function readDeleting():Promise<DeletingReport>{
  const response=await fetch('/api/trash/deleting');
  if(!response.ok)throw new Error('The list of deleted files could not be read. Check that the local service is running.');
  return response.json();
}
export function bytes(value:number){return value<1024**2?`${(value/1024).toFixed(1)} KB`:value<1024**3?`${(value/1024**2).toFixed(1)} MB`:`${(value/1024**3).toFixed(1)} GB`}
function files(count:number){return `${count.toLocaleString()} file${count===1?'':'s'}`}
function parse(value:string){return Date.parse(value.includes('T')?value:value.replace(' ','T')+'Z')}
function removed(value:string){
  const at=parse(value);
  if(!Number.isFinite(at))return '';
  const days=Math.floor((Date.now()-at)/86400000);
  return days<1?'today':`${days}d ago`;
}
export function longDate(value:string){return new Date(parse(value)).toLocaleDateString('en-GB',{day:'numeric',month:'long',year:'numeric'})}
/** When a waiting file goes, in words: "in 12 days", "tomorrow", or "at the next check" once it is due. */
export function countdown(dueAt:string){
  const left=parse(dueAt)-Date.now();
  if(left<=0)return 'at the next check';
  const days=Math.ceil(left/86400000);
  if(days<=1){const hours=Math.ceil(left/3600000);return hours<=1?'within the hour':`in ${hours} hours`}
  return `in ${days} days`;
}

type Mode='bin'|'deleting';

// TrashBoard is the Bin page, and also the Log's list of files deleted from the
// Bin and still waiting out their grace period. Both look like Google Photos'
// Trash: a row-filling grid where a click previews, the round tick selects,
// and the selection's actions sit in the top bar. They differ only in what
// can be done: the Bin restores or deletes, the waiting list restores or
// deletes at once instead of on the day. A batch moves as a whole, sidecars
// and all, so ticking one file ticks its batch.
function TrashBoard({mode,onCount}:{mode:Mode;onCount?:(count:number)=>void}){
  const [items,setItems]=useState<(TrashItem|DeletingItem)[]|null>(null);
  const [report,setReport]=useState<DeletingReport|null>(null);
  const [busy,setBusy]=useState('');
  // The control whose action is running, so the orb shows where the click was.
  const [doing,setDoing]=useState<Control|null>(null);
  const [message,setMessage]=useState('');
  const [error,setError]=useState('');
  const [failures,setFailures]=useState<Result['failures']>([]);
  const [pending,setPending]=useState<Pending|null>(null);
  const dialog=useRef<HTMLDialogElement>(null);

  async function refresh(){
    try{
      const waiting=await readDeleting();
      setReport(waiting);
      let next:(TrashItem|DeletingItem)[]=waiting.items;
      if(mode==='bin'){
        const response=await fetch('/api/trash');
        if(!response.ok)throw new Error('The Bin could not be read. Check that the local service is running.');
        next=await response.json();
      }
      setItems(next);onCount?.(next.length);
    }catch(reason){setError((reason as Error).message)}
  }
  useEffect(()=>{void refresh()},[]);
  useEffect(()=>{
    const element=dialog.current;
    if(!element)return;
    if(pending&&!element.open)element.showModal();
    if(!pending&&element.open)element.close();
  },[pending]);

  const list=items??[];
  const grace=report?.graceDays??0;
  const picks=usePicks(list,item=>item.group);
  const chosen=list.filter(item=>picks.picked.has(item.group));
  const chosenBytes=chosen.reduce((sum,item)=>sum+item.size,0);
  const totalBytes=list.reduce((sum,item)=>sum+item.size,0);
  const allSelected=list.length>0&&chosen.length===list.length;
  const previews:LightboxItem[]=list.filter(item=>item.preview).map(item=>({key:item.key,base:item.preview!,name:item.name,kind:item.kind,detail:caption(item),day:dayOfPath(item.original)??undefined}));
  // Only the Bin's own previews have addresses; the Log's list sits under the
  // Log's grid, which has them already.
  const photo=usePhotoURL(key=>items===null?undefined:previews.some(item=>item.key===key),mode==='bin');

  function caption(item:TrashItem|DeletingItem){
    if('dueAt' in item)return `Deleted ${longDate(item.deletedAt)} · goes ${countdown(item.dueAt)}`;
    return `Removed ${removed(item.removedAt)}`;
  }
  async function act(control:Control,label:string,run:()=>Promise<Result>,done:(result:Result)=>string){
    setBusy(label);setDoing(control);setError('');setMessage('');setFailures([]);
    // Scheduling a deletion takes milliseconds; the orb stays long enough to be
    // seen, so a click never looks as if nothing happened.
    const shown=new Promise(resolve=>setTimeout(resolve,minimumBusy));
    try{
      const result=await run();
      await shown;
      setMessage(done(result));setFailures(result.failures);picks.clear();
    }catch(reason){await shown;setError((reason as Error).message)}
    finally{setBusy('');setDoing(null);await refresh();binChanged()}
  }
  // A control's own text, or the orb while its action runs.
  function face(control:Control,idle:string){
    return doing===control?<Busy label={busy} state="working"/>:idle;
  }
  function restore(keys:string[],control:Control){
    void act(control,'Restoring…',()=>post('/api/trash/restore',{keys}),result=>`${files(result.done)} put back where they came from.`);
  }
  function restoreFile(item:TrashItem){
    void act('lightbox','Restoring…',()=>post('/api/trash/restore-file',{keys:[item.key]}),result=>result.done?`${item.name} is back where it came from, sidecars included. The rest of its batch stays put.`:'');
  }
  function deleted(result:Result){
    if(result.keptDays){
      const until=new Date(Date.now()+result.keptDays*86400000).toLocaleDateString('en-GB',{day:'numeric',month:'long'});
      return `${files(result.done)} deleted from the Bin. They stay on disk until ${until} and can be restored from the Log until then.`;
    }
    return `${files(result.done)} permanently deleted, freeing ${bytes(result.bytes)}.`;
  }
  function consequence(what:string,size:number){
    return grace>0
      ?`${what} (${bytes(size)}) leave the Bin, sidecars included. They stay on disk for ${grace} day${grace===1?'':'s'}, restorable from the Log, and are then deleted automatically.`
      :`${what} (${bytes(size)}) will be permanently deleted, sidecars included. This cannot be undone.`;
  }
  function remove(){
    const keys=chosen.map(item=>item.key),count=chosen.length;
    if(mode==='deleting'){
      setPending({
        title:`Delete ${files(count)} now?`,
        body:`${files(count)} (${bytes(chosenBytes)}) will be permanently deleted now instead of when their grace period ends, sidecars included. This cannot be undone.`,
        confirm:`Delete ${files(count)} now`,
        run:()=>act('delete','Deleting…',()=>post('/api/trash/purge-now',{keys,confirmation:`DELETE ${count}`}),deleted),
      });
      return;
    }
    setPending({
      title:grace>0?`Delete ${files(count)}?`:`Delete ${files(count)} for good?`,
      body:consequence(files(count),chosenBytes),
      confirm:`Delete ${files(count)}`,
      run:()=>act('delete','Deleting…',()=>post('/api/trash/delete',{keys,confirmation:`DELETE ${count}`}),deleted),
    });
  }
  function empty(){
    const count=list.length;
    setPending({
      title:'Empty the Bin?',
      body:consequence(`All ${files(count)} in the Bin`,totalBytes),
      confirm:'Empty the Bin',
      run:()=>act('empty','Emptying…',()=>post('/api/trash/empty',{confirmation:`DELETE ${count}`}),result=>result.keptDays?deleted(result):`The Bin was emptied: ${files(result.done)} permanently deleted, freeing ${bytes(result.bytes)}.`),
    });
  }

  useSelectionBar({count:chosen.length,busy:!!busy,clear:picks.clear,actions:[
    {label:allSelected?'Deselect all':`Select all ${list.length.toLocaleString()}`,icon:'select_all',onClick:()=>allSelected?picks.clear():picks.all()},
    {label:'Restore',icon:'restore_from_trash',onClick:()=>restore(chosen.map(item=>item.key),'restore')},
    {label:mode==='bin'?(grace>0?'Delete':'Delete for good'):'Delete now',icon:'delete_forever',danger:true,onClick:remove},
  ]});

  const summary=items===null?<Busy label={mode==='bin'?'Reading the Bin…':'Reading deleted files…'}/>:<><b>{list.length.toLocaleString()}</b> file{list.length===1?'':'s'} · <b>{bytes(totalBytes)}</b>{chosen.length>0&&<span className="dim"> · {bytes(chosenBytes)} selected</span>}</>;
  const hint=mode==='bin'
    ?(report===null?'':grace>0?<>Deleted files stay on disk for {grace} more day{grace===1?'':'s'}, restorable from the foot of the <a href="/log">Log</a>, then are deleted automatically. <a href="/settings#bin">Change</a></>:<>Deleting from the Bin is immediate and cannot be undone. <a href="/settings#bin">Keep deleted files for a while instead</a></>)
    :(report===null?'':<>Deleted from the Bin, still on disk. Each is deleted automatically {grace} day{grace===1?'':'s'} after it was deleted, checked every {report.checkIntervalMinutes} minutes. <a href="/settings#bin">Change</a></>);
  // On the Log the waiting list only exists while something is waiting; once the
  // last file is restored or deleted, only the sentence saying so remains.
  if(mode==='deleting'&&items!==null&&list.length===0&&!error)return message?<p className="flash" role="status">{message}</p>:null;
  return <>
    <section className={mode==='bin'?'binhead':'binhead deletinghead'}>
      <div className="headrow">
        {mode==='bin'?<h1>Bin</h1>:<h2>Deleted, waiting to go</h2>}
        {mode==='bin'&&list.length>0&&<button type="button" className="textbtn" disabled={!!busy} onClick={empty}>{doing==='empty'?<Busy label={busy} state="working"/>:<><Icon name="delete"/>Empty Bin</>}</button>}
      </div>
      <p className="ysum">{summary}</p>
      {hint&&<p className="hint">{hint}</p>}
      {report?.graceError&&<p className="note warn" role="alert">Automatic deletion is paused: {report.graceError} Save a number of days in <a href="/settings#bin">Settings</a> to resume it.</p>}
    </section>
    {busy&&doing!=='empty'&&doing!=='lightbox'&&<p className="flash" role="status"><Busy label={busy} state="working"/></p>}
    {message&&<p className="flash" role="status">{message}</p>}
    {failures.length>0&&<div className="note warn" role="alert"><b>{files(failures.length)} could not be handled and {failures.length===1?'is':'are'} still {mode==='bin'?'in the Bin':'waiting'}:</b><ul className="plain">{failures.slice(0,20).map((failure,index)=><li key={index}><span className="mono">{failure.name}</span>: {failure.error}</li>)}</ul>{failures.length>20&&<p>and {(failures.length-20).toLocaleString()} more.</p>}</div>}
    {error&&<p className="note warn" role="alert">{error} <button className="btn small" onClick={()=>{setError('');void refresh()}}>Reload</button></p>}
    {mode==='bin'&&items!==null&&list.length===0&&!error&&<p className="note">The Bin is empty. Nothing has been removed, or everything removed has been dealt with.</p>}
    {list.length>0&&<Rows className="bingrid">
      {list.map((item,index)=>{
        const waiting='dueAt' in item?item:null;
        const picked=picks.picked.has(item.group);
        const detail=waiting
          ?`${waiting.lastError?`Could not delete, will retry: ${waiting.lastError}`:`Goes ${countdown(waiting.dueAt)}`} · ${bytes(item.size)}`
          :[bytes(item.size),item.sidecars>0?`+${item.sidecars} sidecar${item.sidecars===1?'':'s'}`:'',item.removedAt?`removed ${removed(item.removedAt)}`:'',item.disk??''].filter(Boolean).join(' · ');
        return <RowTile key={item.key} className={`gone${picked?' picked':''}${waiting&&parse(waiting.dueAt)<=Date.now()?' due':''}`} title={`${item.name}\n${item.original}\n${detail}`}
          onClick={event=>{if(event.shiftKey&&picks.picked.size>0)picks.toggle(index,true);else if(item.preview)photo.show(item.key);else picks.toggle(index,false)}}>
          {item.preview?<FilePreview base={item.preview} name={item.name} kind={item.kind}/>
            :<div className="media-missing"><span>{item.kind==='sidecar'?'Sidecar':'Preview unavailable'}</span><small>{item.name.split('.').pop()?.toUpperCase()}{item.kind==='sidecar'?' · its photograph has already left':''}</small></div>}
          <Pick checked={picked} label={`Select ${item.name}, ${detail}`} onToggle={extend=>picks.toggle(index,extend)}/>
          {item.kind==='video'&&<span className="dur"><Icon name="play_circle"/></span>}
          {waiting&&<div className="bdg"><span className={`b${waiting.lastError?' warn':''}`}>{waiting.lastError?'Could not delete':`Goes ${countdown(waiting.dueAt)}`}</span></div>}
        </RowTile>})}
    </Rows>}
    {photo.open!==null&&previews.length>0&&<Lightbox items={previews} initialKey={photo.open} onClose={photo.close} onMove={photo.moved} actions={current=>{
      // The writer's own batches give back one photograph at a time; the other
      // engines move a batch only as a whole, so the button says so.
      const item=list.find(other=>other.key===current.key);
      if(!item)return null;
      const batch=list.filter(other=>other.group===item.group).length;
      if(item.source==='bin'||batch===1)return <button type="button" className="rvbtn" disabled={!!busy} onClick={()=>restoreFile(item)}>{face('lightbox','Restore this file')}</button>;
      return <button type="button" className="rvbtn" disabled={!!busy} onClick={()=>restore([current.key],'lightbox')}>{face('lightbox',`Restore with its batch (${batch} files)`)}</button>;
    }}/>}
    <dialog ref={dialog} className="confirm" aria-labelledby={`confirm-title-${mode}`} onClose={()=>setPending(null)} onClick={event=>{if(event.target===event.currentTarget)setPending(null)}}>
      {pending&&<form method="dialog" onSubmit={event=>{event.preventDefault();const run=pending.run;setPending(null);void run()}}>
        <h2 id={`confirm-title-${mode}`}>{pending.title}</h2>
        <p>{pending.body}</p>
        <div className="confirmacts">
          <button type="button" className="btn" autoFocus onClick={()=>setPending(null)}>Cancel</button>
          <button type="submit" className="btn danger">{pending.confirm}</button>
        </div>
      </form>}
    </dialog>
  </>;
}

export function Bin({onCount}:{onCount?:(count:number)=>void}){return <TrashBoard mode="bin" onCount={onCount}/>}
/** The Log's list of files deleted from the Bin and not yet gone. Renders nothing when there are none. */
export function Deleting(){return <TrashBoard mode="deleting"/>}
