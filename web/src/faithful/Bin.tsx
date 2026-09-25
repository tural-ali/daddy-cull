import {useEffect,useRef,useState,type MouseEvent} from 'react';
import {FilePreview} from '../Media';
import {Busy} from '../Busy';

/** One card in the Bin, whichever tool put the file there. */
export type TrashItem={key:string;group:string;source:'marked'|'bin'|'legacy'|'screenshot';name:string;original:string;kind:string;size:number;sidecars:number;removedAt:string;preview?:string;disk?:string};
type Result={done:number;bytes:number;failures:{name:string;error:string}[]};
type Pending={title:string;body:string;confirm:string;run:()=>Promise<void>};

async function post(path:string,body:unknown):Promise<Result>{
  const response=await fetch(path,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});
  const text=await response.text();
  let result:unknown;
  try{result=JSON.parse(text)}catch{throw new Error('The Bin could not confirm what happened. Reload the page to see what it holds now.')}
  if(!response.ok)throw new Error((result as {error?:string}).error||'The Bin request failed.');
  return result as Result;
}
function bytes(value:number){return value<1024**2?`${(value/1024).toFixed(1)} KB`:value<1024**3?`${(value/1024**2).toFixed(1)} MB`:`${(value/1024**3).toFixed(1)} GB`}
function files(count:number){return `${count.toLocaleString()} file${count===1?'':'s'}`}
function removed(value:string){
  const at=Date.parse(value.includes('T')?value:value.replace(' ','T')+'Z');
  if(!Number.isFinite(at))return '';
  const days=Math.floor((Date.now()-at)/86400000);
  return days<1?'today':`${days}d ago`;
}
// full opens the file at full size in a new tab, as the earlier tool did. A
// still goes through the large preview because a HEIC or RAW original will not
// open in a browser; a clip is streamed as it is.
function full(item:TrashItem){return item.kind==='video'?`${item.preview}/original`:`${item.preview}/preview?size=large`}

export function Bin({onCount}:{onCount?:(count:number)=>void}){
  const [items,setItems]=useState<TrashItem[]|null>(null);
  const [selected,setSelected]=useState<Set<string>>(new Set());
  const [anchor,setAnchor]=useState<string|null>(null);
  const [busy,setBusy]=useState('');
  const [message,setMessage]=useState('');
  const [error,setError]=useState('');
  const [failures,setFailures]=useState<Result['failures']>([]);
  const [pending,setPending]=useState<Pending|null>(null);
  const dialog=useRef<HTMLDialogElement>(null);

  async function refresh(){
    try{
      const response=await fetch('/api/trash');
      if(!response.ok)throw new Error('The Bin could not be read. Check that the local service is running.');
      const next:TrashItem[]=await response.json();
      setItems(next);onCount?.(next.length);
      // A selection only ever names what the Bin still holds.
      const groups=new Set(next.map(item=>item.group));
      setSelected(current=>new Set([...current].filter(group=>groups.has(group))));
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
  // A batch moves as a whole, sidecars and all, so its cards are selected together.
  const chosen=list.filter(item=>selected.has(item.group));
  const chosenBytes=chosen.reduce((sum,item)=>sum+item.size,0);
  const totalBytes=list.reduce((sum,item)=>sum+item.size,0);
  const allSelected=list.length>0&&chosen.length===list.length;

  function toggle(item:TrashItem,event:MouseEvent){
    const next=new Set(selected);
    if(event.shiftKey&&anchor){
      const from=list.findIndex(other=>other.key===anchor),to=list.findIndex(other=>other.key===item.key);
      if(from>=0&&to>=0){
        if(!(event.metaKey||event.ctrlKey))next.clear();
        for(let index=Math.min(from,to);index<=Math.max(from,to);index++)next.add(list[index].group);
        setSelected(next);return;
      }
    }
    if(next.has(item.group))next.delete(item.group);else next.add(item.group);
    setSelected(next);setAnchor(item.key);
  }
  async function act(label:string,run:()=>Promise<Result>,done:(result:Result)=>string){
    setBusy(label);setError('');setMessage('');setFailures([]);
    try{
      const result=await run();
      setMessage(done(result));setFailures(result.failures);setSelected(new Set());
    }catch(reason){setError((reason as Error).message)}
    finally{setBusy('');await refresh()}
  }
  function restore(keys:string[]){
    void act('Restoring…',()=>post('/api/trash/restore',{keys}),result=>`${files(result.done)} put back where they came from.`);
  }
  function remove(){
    const keys=chosen.map(item=>item.key),count=chosen.length;
    setPending({
      title:`Delete ${files(count)} for good?`,
      body:`${files(count)} (${bytes(chosenBytes)}) will be permanently deleted, sidecars included. This cannot be undone.`,
      confirm:`Delete ${files(count)}`,
      run:()=>act('Deleting…',()=>post('/api/trash/delete',{keys,confirmation:`DELETE ${count}`}),result=>`${files(result.done)} permanently deleted, freeing ${bytes(result.bytes)}.`),
    });
  }
  function empty(){
    const count=list.length;
    setPending({
      title:'Empty the Bin?',
      body:`All ${files(count)} in the Bin (${bytes(totalBytes)}) will be permanently deleted, sidecars included. This cannot be undone.`,
      confirm:'Empty the Bin',
      run:()=>act('Emptying…',()=>post('/api/trash/empty',{confirmation:`DELETE ${count}`}),result=>`The Bin was emptied: ${files(result.done)} permanently deleted, freeing ${bytes(result.bytes)}.`),
    });
  }

  return <>
    <section className="binhead">
      <h1>Bin</h1>
      <p className="ysum">{items===null?<Busy label="Reading the Bin…"/>:<><b>{list.length.toLocaleString()}</b> file{list.length===1?'':'s'} · <b>{bytes(totalBytes)}</b></>}</p>
      <p className="hint">Removed files are kept here so a mistake costs nothing. Nothing is deleted automatically.</p>
      {list.length>0&&<div className="binacts">
        <button className="btn" disabled={!!busy} onClick={()=>restore(list.map(item=>item.key))}>Restore everything</button>
        <button className="btn danger ghosty" disabled={!!busy} onClick={empty}>Empty the Bin</button>
      </div>}
    </section>
    {message&&<p className="flash" role="status">{message}</p>}
    {failures.length>0&&<div className="note warn" role="alert"><b>{files(failures.length)} could not be handled and {failures.length===1?'is':'are'} still in the Bin:</b><ul className="plain">{failures.slice(0,20).map((failure,index)=><li key={index}><span className="mono">{failure.name}</span>: {failure.error}</li>)}</ul>{failures.length>20&&<p>and {(failures.length-20).toLocaleString()} more.</p>}</div>}
    {error&&<p className="note warn" role="alert">{error} <button className="btn small" onClick={()=>{setError('');void refresh()}}>Reload the Bin</button></p>}
    {items!==null&&list.length===0&&!error&&<p className="note">The Bin is empty. Nothing has been removed, or everything removed has been dealt with.</p>}
    {list.length>0&&<div className="gal tight bingal">
      {list.map(item=><figure key={item.key} className={`mo gone${selected.has(item.group)?' sel':''}`} onClick={event=>{if(event.metaKey||event.ctrlKey||event.shiftKey){event.preventDefault();toggle(item,event)}}}>
        <button type="button" role="checkbox" className="tick" aria-checked={selected.has(item.group)} aria-label={`Select ${item.name}`} onClick={event=>{event.stopPropagation();toggle(item,event)}}><span/></button>
        {item.preview?<a className="shot" href={full(item)} target="_blank" rel="noopener" onClick={event=>{if(event.metaKey||event.ctrlKey||event.shiftKey)event.preventDefault()}}><FilePreview base={item.preview} name={item.name} kind={item.kind}/></a>
          :<div className="media-missing"><span>{item.kind==='sidecar'?'Sidecar':'Preview unavailable'}</span><small>{item.name.split('.').pop()?.toUpperCase()}{item.kind==='sidecar'?' · its photograph has already left':''}</small></div>}
        <figcaption className="cap stack">
          <span title={item.original}>{item.name}</span>
          <span className="dim" title={`Removed ${new Date(Date.parse(item.removedAt)).toLocaleString()}`}>{bytes(item.size)}{item.sidecars>0&&` · +${item.sidecars} sidecar${item.sidecars===1?'':'s'}`}{item.removedAt&&` · ${removed(item.removedAt)}`}{item.disk&&` · ${item.disk}`}</span>
        </figcaption>
      </figure>)}
    </div>}
    {list.length>0&&<footer className="fbar">
      <span className="fleft">
        <label className="selall"><input type="checkbox" checked={allSelected} disabled={!!busy} onChange={()=>setSelected(allSelected?new Set():new Set(list.map(item=>item.group)))}/> Select all</label>
        <span className="sel"><span id="selcount">{chosen.length.toLocaleString()}</span> selected{chosen.length>0&&` · ${bytes(chosenBytes)}`}</span>
        <button className="btn" disabled={!!busy||chosen.length===0} onClick={()=>restore(chosen.map(item=>item.key))}>Restore selected</button>
        <button className="btn danger" disabled={!!busy||chosen.length===0} onClick={remove}>Delete selected for good</button>
      </span>
      <span className="fright hint">{busy?<Busy label={busy} state="working"/>:'Shift-click selects a run. A file that cannot be deleted is reported, never counted as gone.'}</span>
    </footer>}
    <dialog ref={dialog} className="confirm" aria-labelledby="confirm-title" onClose={()=>setPending(null)} onClick={event=>{if(event.target===event.currentTarget)setPending(null)}}>
      {pending&&<form method="dialog" onSubmit={event=>{event.preventDefault();const run=pending.run;setPending(null);void run()}}>
        <h2 id="confirm-title">{pending.title}</h2>
        <p>{pending.body}</p>
        <div className="confirmacts">
          <button type="button" className="btn" autoFocus onClick={()=>setPending(null)}>Cancel</button>
          <button type="submit" className="btn danger">{pending.confirm}</button>
        </div>
      </form>}
    </dialog>
  </>;
}
