import {useEffect,useRef,useState} from 'react';
import {type Asset,type Saved,type Status,get} from './api';
import {Media} from './Media';
type Change={file:Asset;status:Status};
type Job={assetId:number;status:Status;favourite:boolean;expectedRevision:number;requestId:string};
const requestId=()=>Array.from(crypto.getRandomValues(new Uint8Array(16)),b=>b.toString(16).padStart(2,'0')).join('');
export function Compare({asset,onClose,onChanged,onNext,related=false}:{related?:boolean;asset:Asset;onClose:()=>void;onChanged:(changes:Asset[])=>void;onNext?:()=>void}){
 const [files,setFiles]=useState<Asset[]>([]),[error,setError]=useState(''),[busy,setBusy]=useState(false);
 const [focus,setFocus]=useState(0),[scale,setScale]=useState(1),[pan,setPan]=useState({x:0,y:0});
 const [proposal,setProposal]=useState<Change[]|null>(null),[undo,setUndo]=useState<Change[]|null>(null);
 const [ready,setReady]=useState<Set<number>>(new Set());
 const [selected,setSelected]=useState<Set<number>>(new Set());
 const journal=useRef('cull.group.pending.'+requestId());
 const pending=useRef<{jobs:Job[];changes:Change[];isUndo:boolean}|null>(null);
 const [uncertain,setUncertain]=useState(false);
 const drag=useRef<{x:number;y:number;px:number;py:number}|null>(null);
 useEffect(()=>{const a=new AbortController();setFiles([]);setFocus(0);setUndo(null);setProposal(null);setSelected(new Set());setError('');setScale(1);setPan({x:0,y:0});get<Asset[]>(`/api/assets/${asset.id}/${related?'related':'alternatives'}`,a.signal).then(setFiles).catch(e=>{if(!a.signal.aborted)setError(e.message)});return()=>a.abort()},[asset.id,related]);
 async function send(changes:Change[],isUndo=false){
  if(busy||(!changes.length&&!pending.current))return;
  if(!pending.current)pending.current={changes,isUndo,jobs:changes.map(({file,status})=>({assetId:file.id,status,favourite:file.favourite,expectedRevision:file.revision,requestId:requestId()}))};
  const task=pending.current;setBusy(true);setError('');
  try{
   localStorage.setItem(journal.current,JSON.stringify(task.jobs));
   const r=await fetch('/api/decisions/batch',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(task.jobs)});
   if(!r.ok){if(r.status===409){localStorage.removeItem(journal.current);pending.current=null;setUncertain(false);throw Error('A decision changed elsewhere. Close and reopen this comparison before choosing again.')}throw Error('Save was not confirmed. Retry uses the same request and cannot apply it twice.')}
   const results=await r.json() as Saved[];
   const updated=task.changes.map((c,i)=>({...c.file,status:c.status,revision:results[i].revision}));
   setFiles(fs=>fs.map(f=>updated.find(a=>a.id===f.id)??f));
   setUndo(task.isUndo?null:task.changes.map((c,i)=>({file:{...c.file,status:c.status,revision:results[i].revision,favourite:results[i].previousFavourite},status:results[i].previousStatus as Status})));
   localStorage.removeItem(journal.current);pending.current=null;setUncertain(false);setProposal(null);onChanged(updated);
  }catch(e){setError((e as Error).message);setUncertain(!!pending.current)}finally{setBusy(false)}
 }
 function choose(f:Asset,status:Status){if(!busy&&!uncertain&&ready.has(f.id))void send([{file:f,status}])}
 useEffect(()=>{function key(e:KeyboardEvent){if(busy||uncertain||proposal||!files.length||e.repeat||e.metaKey||e.ctrlKey||e.altKey||e.target instanceof HTMLInputElement||e.target instanceof HTMLSelectElement)return;
  const k=e.key.toLowerCase();if(['k','x','l','u','arrowright','arrowleft','enter','1','2','3','4','5','6','7','8','9'].includes(k))e.preventDefault();
  if(k==='k')choose(files[focus],'keep');if(k==='x')choose(files[focus],'cull');if(k==='l')choose(files[focus],'later');if(k==='u'&&undo)void send(undo,true);
  if(k==='arrowright')setFocus(i=>Math.min(i+1,files.length-1));if(k==='arrowleft')setFocus(i=>Math.max(i-1,0));if(/^[1-9]$/.test(k))setFocus(Math.min(Number(k)-1,files.length-1));
  if(k==='enter'&&files.every(f=>f.status!=='unreviewed'))(onNext??onClose)();
 };window.addEventListener('keydown',key);return()=>window.removeEventListener('keydown',key)});
 useEffect(()=>{const leave=(e:BeforeUnloadEvent)=>{if(pending.current){e.preventDefault();e.returnValue=''}};window.addEventListener('beforeunload',leave);return()=>window.removeEventListener('beforeunload',leave)},[]);
 const locked=busy||uncertain;
 const visible=files.length<=2?files:files.filter((_,i)=>i===focus||i===(focus===0?1:0));
 const remaining=files.filter(f=>f.status==='unreviewed').length;
 function propose(status:Status){const choices=files.filter(f=>selected.has(f.id));if(choices.length) setProposal(choices.map(file=>({file,status})))}
 return <section className="comparison" aria-label={related?'Compare possible duplicates':'Compare possible upgrade'}>
 <div className="batchhead"><div><h2>{related?'Compare related copies':'Same memory, a better copy?'}</h2><p>{related?'Filename relationship: possible copies, edits or companion files. Not verified as byte-identical.':'Possible upgrade from the existing Takeout report. Inspect before choosing.'}</p></div><button disabled={locked} onClick={onClose}>Close comparison</button></div>
 <div className="compare-toolbar"><span>{remaining} undecided · {files.length} files</span><button disabled={!undo||locked} onClick={()=>undo&&void send(undo,true)}>Undo group decision <kbd>U</kbd></button><label>Linked preview zoom <select value={scale} onChange={e=>{setScale(Number(e.target.value));setPan({x:0,y:0})}}><option value={1}>Fit</option><option value={2}>2×</option><option value={4}>4×</option></select></label><span>Zoom magnifies the preview; RAW focus needs a full-detail renderer.</span></div>
 {error&&<div role="alert" className="error">{error}{uncertain&&<button disabled={busy} onClick={()=>void send([])}>Retry same save</button>}</div>}
 {files.length>2&&<div className="variant-strip">{files.map((f,i)=><button key={f.id} aria-pressed={i===focus} onClick={()=>setFocus(i)}>{i+1}. {f.path.split('/').pop()} · {f.status}</button>)}</div>}
 <div className="compare-grid">{visible.map(f=><article key={f.id} className={files[focus]?.id===f.id?'focused':''} onClick={()=>setFocus(files.findIndex(a=>a.id===f.id))}>
 <h3>{f.path.split('/').pop()}</h3><div className="stage zoom-stage" onPointerDown={e=>{if(scale===1||f.kind==='video')return;e.currentTarget.setPointerCapture(e.pointerId);drag.current={x:e.clientX,y:e.clientY,px:pan.x,py:pan.y}}} onPointerMove={e=>{if(drag.current)setPan({x:drag.current.px+e.clientX-drag.current.x,y:drag.current.py+e.clientY-drag.current.y})}} onPointerUp={()=>drag.current=null} onLostPointerCapture={()=>drag.current=null}><div className="zoom-content" style={{transform:`translate(${pan.x}px,${pan.y}px) scale(${scale})`}}><Media asset={f} large onReady={id=>setReady(s=>s.has(id)?s:new Set(s).add(id))}/></div></div>
 <p>{f.source==='archive'?'Archive':'Google Takeout'} · {f.kind.toUpperCase()} · {(f.size/1048576).toFixed(2)} MB</p><details><summary>File location</summary><p className="path">{f.path}</p></details>
 <p>Choice: {f.status==='unreviewed'?'Not decided':f.status==='cull'?'Marked for culling':f.status}</p><div className="compare-actions"><button disabled={locked||!ready.has(f.id)} className="primary" onClick={e=>{e.stopPropagation();choose(f,'keep')}}>Keep this file</button><button disabled={locked||!ready.has(f.id)} onClick={e=>{e.stopPropagation();choose(f,'later')}}>Decide later</button><button disabled={locked||!ready.has(f.id)} className="cull" onClick={e=>{e.stopPropagation();choose(f,'cull')}}>Mark this file for culling</button></div></article>)}</div>
 <div className="compare-toolbar"><p>K / X / L applies to the outlined file. Arrow keys change focus.</p><button disabled={locked||!files.length||files.length>20} onClick={()=>setProposal(files.map(file=>({file,status:'keep'})))}>Keep {files.length===2?'both':'all'} files</button><button disabled={locked||remaining>20||!remaining} onClick={()=>setProposal(files.filter(f=>f.status==='unreviewed').map(file=>({file,status:'later'})))}>Decide remaining later</button><button className="primary" disabled={locked||remaining>0||!files.length} onClick={onNext??onClose}>Next memory <kbd>Enter</kbd></button></div>
 <details className="library-note"><summary>Choose files for a group decision</summary>{files.map(f=><label className="group-select" key={f.id}><input type="checkbox" checked={selected.has(f.id)} disabled={locked||(!selected.has(f.id)&&selected.size>=20)} onChange={()=>setSelected(s=>{const n=new Set(s);n.has(f.id)?n.delete(f.id):n.add(f.id);return n})}/>{f.path.split('/').pop()} · {f.status}</label>)}<button disabled={locked||!selected.size} onClick={()=>propose('keep')}>Review keeping {selected.size}</button> <button disabled={locked||!selected.size} onClick={()=>propose('cull')}>Review culling {selected.size}</button></details>
 {proposal&&<div className="group-proposal" role="region" aria-label="Confirm group decisions"><h3>Review {proposal.length} file choices</h3><ul>{proposal.map(c=><li key={c.file.id}>{c.file.path.split('/').pop()} → {c.status==='cull'?'Mark for culling':c.status}</li>)}</ul><p>This saves choices only. No files move.</p><button disabled={locked} className="primary" onClick={()=>void send(proposal)}>Save these {proposal.length} choices</button> <button disabled={locked} onClick={()=>setProposal(null)}>Cancel</button></div>}
 </section>
}
