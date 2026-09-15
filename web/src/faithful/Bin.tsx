import {useEffect,useState} from 'react';
import {get,type Asset,type Page} from '../api';
import {Media} from '../Media';
import type {ScreenshotPlan} from './Screenshots';

type Plan={id:string;state:string;created:string;assets:{id:number;path:string}[];files:{original:string;size:number;sidecar:boolean;phase:string}[];warnings:string[];error?:string};
type LegacyBinItem={id:number;batch:string;kind:string;original:string;stored:string;day?:string;size:number;reason?:string;culledAt:string;restoredAt?:string;purgedAt?:string};
type LegacyPlanFile=LegacyBinItem&{hash:string;mtime:number;phase:string};
type LegacyPlan={id:string;state:string;created:string;files:LegacyPlanFile[];error?:string};
async function post<T>(path:string,body:unknown):Promise<T>{
  const response=await fetch(path,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});
  const text=await response.text();
  let result:unknown;
  try{result=JSON.parse(text)}catch{throw new Error('The Bin result could not be confirmed. Refresh before retrying.')}
  if(!response.ok)throw new Error((result as {error?:string}).error||'The Bin request failed.');
  return result as T;
}
function bytes(value:number){return value<1024**2?`${(value/1024).toFixed(1)} KB`:value<1024**3?`${(value/1024**2).toFixed(1)} MB`:`${(value/1024**3).toFixed(1)} GB`}

export function Bin(){
  const [marked,setMarked]=useState<Asset[]>([]);
  const [plans,setPlans]=useState<Plan[]>([]);
  const [legacy,setLegacy]=useState<LegacyBinItem[]>([]);
  const [legacySelection,setLegacySelection]=useState<Set<number>>(new Set());
  const [legacyPlan,setLegacyPlan]=useState<LegacyPlan|null>(null);
  const [legacyAction,setLegacyAction]=useState<'restore'|'purge'>('restore');
  const [legacyConfirmation,setLegacyConfirmation]=useState('');
  const [screenshotPlans,setScreenshotPlans]=useState<ScreenshotPlan[]>([]);
  const [screenshotPurge,setScreenshotPurge]=useState<ScreenshotPlan|null>(null);
  const [screenshotConfirmation,setScreenshotConfirmation]=useState('');
  const [selection,setSelection]=useState<Set<number>>(new Set());
  const [plan,setPlan]=useState<Plan|null>(null);
  const [action,setAction]=useState<'quarantine'|'restore'|'purge'>('quarantine');
  const [confirmation,setConfirmation]=useState('');
  const [writerUp,setWriterUp]=useState(true);
  const [busy,setBusy]=useState(false);
  const [message,setMessage]=useState('');
  const [error,setError]=useState('');
  // Each section is loaded on its own. The Bin writer is a separate process and
  // is often not running, but that says nothing about the catalogue: folding all
  // four reads into one Promise.all meant a writer that was merely absent threw
  // away the marked list the page had already fetched successfully, and reported
  // it as the catalogue being down.
  async function refresh(){
    const [markedPage,history,oldHistory,shotHistory]=await Promise.allSettled([
      get<Page>('/api/assets?view=review&status=cull&limit=200&groups=0'),
      get<Plan[]>('/api/bin'),
      get<LegacyBinItem[]>('/api/legacy-bin'),
      get<ScreenshotPlan[]>('/api/screenshot-bin'),
    ]);
    const failures:string[]=[];
    if(markedPage.status==='fulfilled')setMarked(markedPage.value.assets.filter(asset=>asset.source==='archive'));
    else failures.push('the catalogue');
    if(history.status==='fulfilled'){setPlans(history.value);setWriterUp(true)}
    else setWriterUp(false);
    if(oldHistory.status==='fulfilled')setLegacy(oldHistory.value);
    else failures.push('the imported Bin history');
    if(shotHistory.status==='fulfilled')setScreenshotPlans(shotHistory.value);
    else failures.push('the screenshot Bin');
    setError(failures.length>0?`Could not load ${failures.join(' or ')}. Check that the local service is running.`:'');
  }
  useEffect(()=>{void refresh()},[]);
  async function preview(){
    if(selection.size===0)return;
    setBusy(true);setError('');
    try{setPlan(await post<Plan>('/api/bin/preview',{ids:[...selection]}));setAction('quarantine');setConfirmation('')}catch(reason){setError((reason as Error).message)}finally{setBusy(false)}
  }
  async function execute(){
    if(!plan)return;
    setBusy(true);setError('');
    try{
      const result=await post<Plan>('/api/bin/execute',{id:plan.id,action,confirmation});
      setMessage(action==='quarantine'?'The selected files are in the Bin and can be restored.':action==='restore'?'The files are back at their original paths.':'The confirmed Bin files were permanently deleted.');
      setSelection(new Set());setPlan(null);setPlans(current=>[result,...current.filter(item=>item.id!==result.id)]);await refresh();
    }catch(reason){setError((reason as Error).message);await refresh()}finally{setBusy(false)}
  }
  function inspect(selected:Plan,nextAction:'restore'|'purge'|'quarantine'){setPlan(selected);setAction(nextAction);setConfirmation('');setError('')}
  async function previewLegacy(){
    if(legacySelection.size===0)return;
    setBusy(true);setError('');
    try{setLegacyPlan(await post<LegacyPlan>('/api/legacy-bin/preview',{ids:[...legacySelection]}));setLegacyAction('restore');setLegacyConfirmation('')}catch(reason){setError((reason as Error).message)}finally{setBusy(false)}
  }
  async function executeLegacy(){
    if(!legacyPlan)return;
    setBusy(true);setError('');
    try{
      await post<LegacyPlan>('/api/legacy-bin/execute',{id:legacyPlan.id,action:legacyAction,confirmation:legacyConfirmation});
      setMessage(legacyAction==='restore'?'The selected legacy batch is back at its original disk paths.':'The exact confirmed legacy Bin files were permanently deleted.');
      setLegacyPlan(null);setLegacySelection(new Set());setLegacyConfirmation('');await refresh();
    }catch(reason){setError((reason as Error).message);await refresh()}finally{setBusy(false)}
  }
  async function undoScreenshot(plan:ScreenshotPlan){
    setBusy(true);setError('');
    try{await post<ScreenshotPlan>('/api/screenshot-actions/undo',{id:plan.id});setMessage('The screenshot is back in the holding area.');await refresh()}catch(reason){setError((reason as Error).message);await refresh()}finally{setBusy(false)}
  }
  async function purgeScreenshot(){
    if(!screenshotPurge)return;
    setBusy(true);setError('');
    try{await post<ScreenshotPlan>('/api/screenshot-actions/purge',{id:screenshotPurge.id,confirmation:screenshotConfirmation});setMessage('The exact confirmed screenshot Bin files were permanently deleted.');setScreenshotPurge(null);setScreenshotConfirmation('');await refresh()}catch(reason){setError((reason as Error).message);await refresh()}finally{setBusy(false)}
  }
  const active=plans.filter(item=>!['restored','purged','purged_recovered'].includes(item.state));
  const legacyActive=legacy.filter(item=>!item.restoredAt&&!item.purgedAt);
  const selectedFiles=plan?.files.length??0;
  return <>
    <section className="binhead"><h1>Bin</h1><p className="ysum"><b>{marked.length.toLocaleString()}</b> marked in the archive · <b>{writerUp?(active.length+legacyActive.length).toLocaleString():'unknown'}</b> active Bin items</p><p className="hint">Marking is a review choice. Files only move after this page shows the exact manifest and you approve it.</p></section>
    {message&&<p className="flash" role="status">{message}</p>}{error&&<p className="note warn" role="alert">{error} <button className="btn small" onClick={()=>void refresh()}>Refresh</button></p>}
    {plan?<section className="xgroup"><h2>{action==='quarantine'?'Review the exact files to move':action==='restore'?'Restore these files to their original paths':'Permanently delete these Bin files'}</h2><p>{plan.assets.length} media files and {plan.files.filter(file=>file.sidecar).length} sidecars · {bytes(plan.files.reduce((total,file)=>total+file.size,0))}</p><ul className="plain mono">{plan.files.map(file=><li key={file.original}>{file.original} <span className="dim">· {file.sidecar?'sidecar':'media'} · {bytes(file.size)} · {file.phase}</span></li>)}</ul>{plan.warnings.map(warning=><p className="note warn" key={warning}>{warning}</p>)}{action==='purge'&&<label className="note warn">This cannot be undone. Type <strong>DELETE {selectedFiles}</strong> to continue.<input value={confirmation} onChange={event=>setConfirmation(event.target.value)} autoComplete="off"/></label>}<p className="binacts"><button className={`btn ${action==='purge'?'danger':'primary'}`} disabled={busy||(action==='purge'&&confirmation!==`DELETE ${selectedFiles}`)} onClick={()=>void execute()}>{action==='quarantine'?`Move ${selectedFiles} files to Bin`:action==='restore'?`Restore ${selectedFiles} files`:`Permanently delete ${selectedFiles} files`}</button><button className="btn" disabled={busy} onClick={()=>setPlan(null)}>Cancel</button></p></section>:<>
      <h2 className="binsec">Marked for culling</h2><p className="hint">Select up to 20 archive files. Google Takeout source files are never offered here.</p><div className="gal tight">{marked.map(asset=><figure className={`mo${selection.has(asset.id)?' sel':''}`} key={asset.id} onClick={()=>setSelection(current=>{const next=new Set(current);next.has(asset.id)?next.delete(asset.id):next.size<20&&next.add(asset.id);return next})}><Media asset={asset}/><input className="tick" type="checkbox" checked={selection.has(asset.id)} readOnly/><figcaption className="cap"><span>{asset.path.split('/').pop()}</span><span className="dim">{bytes(asset.size)}</span></figcaption></figure>)}</div>{marked.length===0&&<p className="note">No archive files are waiting to move.</p>}{!writerUp&&<p className="note warn">The Bin writer is not running, so nothing can be moved right now. Marking still works and is recorded here; the files stay exactly where they are. Start the writer process with its archive root and key to carry these out.</p>}<p className="binacts"><button className="btn primary" disabled={busy||selection.size===0||!writerUp} onClick={()=>void preview()}>Review {selection.size} selected</button></p>
      <h2 className="binsec">Files already in the Bin</h2>{!writerUp?<p className="note">The Bin writer is not running, so what it holds cannot be read. This is not a statement that the Bin is empty.</p>:active.length===0?<p className="note">The Bin is empty. Marked photos remain in the archive until moved.</p>:active.map(item=><article className="xgroup" key={item.id}><h3>{item.assets.length} media · {item.files.length} files including sidecars</h3><p className="dim">{new Date(item.created).toLocaleString()} · {item.state}</p><ul className="plain mono">{item.assets.map(asset=><li key={asset.id}>{asset.path}</li>)}</ul>{item.error&&<p className="note warn">{item.error}</p>}<p className="binacts">{item.state==='bin'?<><button className="btn" disabled={busy} onClick={()=>inspect(item,'restore')}>Review restore</button><button className="btn danger ghosty" disabled={busy} onClick={()=>inspect(item,'purge')}>Review permanent deletion</button></>:<button className="btn" disabled={busy} onClick={()=>inspect(item,item.state==='restoring'?'restore':item.state==='purging'?'purge':'quarantine')}>Review interrupted operation</button>}</p></article>)}
      {screenshotPlans.length>0&&<><h2 className="binsec">Screenshots in the Bin</h2>{screenshotPurge&&<section className="xgroup"><h3>Permanently delete these screenshot files</h3><ul className="plain mono">{screenshotPurge.files.map(file=><li key={file.destination}>{file.destination} <span className="dim">· {bytes(file.size)}</span></li>)}</ul><label className="note warn">This cannot be undone. Type <strong>DELETE {screenshotPurge.files.length}</strong> to continue.<input value={screenshotConfirmation} onChange={event=>setScreenshotConfirmation(event.target.value)} autoComplete="off"/></label><p className="binacts"><button className="btn danger" disabled={busy||screenshotConfirmation!==`DELETE ${screenshotPurge.files.length}`} onClick={()=>void purgeScreenshot()}>Permanently delete these files</button><button className="btn" disabled={busy} onClick={()=>{setScreenshotPurge(null);setScreenshotConfirmation('')}}>Cancel</button></p></section>}{screenshotPlans.map(item=><article className="xgroup" key={`shot-${item.id}`}><h3>{item.files[0].source}</h3><p>{item.files.length} file{item.files.length===1?'':'s'} including sidecars · {bytes(item.files.reduce((sum,file)=>sum+file.size,0))}</p><p className="dim">Removed {new Date(item.created).toLocaleString()}</p>{item.error&&<p className="note warn">{item.error}</p>}<p className="binacts"><button className="btn" disabled={busy} onClick={()=>void undoScreenshot(item)}>Undo remove</button><button className="btn danger ghosty" disabled={busy} onClick={()=>{setScreenshotPurge(item);setScreenshotConfirmation('')}}>Review permanent deletion</button></p></article>)}</>}
      {legacy.length>0&&<><h2 className="binsec">Bin history imported from the earlier PHP tool</h2><p className="hint">All {legacy.length.toLocaleString()} recorded items are preserved. Select an active item to inspect its exact stored bytes. Items from the same original cull action travel together, including sidecars.</p>{legacyPlan?<section className="xgroup"><h3>Review the exact legacy files</h3><p>{legacyPlan.files.length} files · {bytes(legacyPlan.files.reduce((sum,file)=>sum+file.size,0))}</p><ul className="plain mono">{legacyPlan.files.map(file=><li key={file.id}>{file.stored} <span className="dim">· {file.kind} · {bytes(file.size)}</span></li>)}</ul><p className="binacts"><button className={`btn ${legacyAction==='restore'?'primary':''}`} onClick={()=>{setLegacyAction('restore');setLegacyConfirmation('')}}>Restore to original paths</button><button className={`btn ${legacyAction==='purge'?'danger':''}`} onClick={()=>setLegacyAction('purge')}>Permanently delete</button></p>{legacyAction==='purge'&&<label className="note warn">This cannot be undone. Type <strong>DELETE {legacyPlan.files.length}</strong> to continue.<input value={legacyConfirmation} onChange={event=>setLegacyConfirmation(event.target.value)} autoComplete="off"/></label>}<p className="binacts"><button className={`btn ${legacyAction==='purge'?'danger':'primary'}`} disabled={busy||(legacyAction==='purge'&&legacyConfirmation!==`DELETE ${legacyPlan.files.length}`)} onClick={()=>void executeLegacy()}>{legacyAction==='restore'?'Restore these files':'Permanently delete these files'}</button><button className="btn" disabled={busy} onClick={()=>setLegacyPlan(null)}>Cancel</button></p></section>:<>{legacyActive.map(item=><article className={`xgroup${legacySelection.has(item.id)?' sel':''}`} key={`legacy-${item.id}`}><label><input type="checkbox" checked={legacySelection.has(item.id)} onChange={()=>setLegacySelection(current=>{const next=new Set(current);next.has(item.id)?next.delete(item.id):next.size<20&&next.add(item.id);return next})}/> <strong>{item.original.split('/').pop()}</strong></label><p>{bytes(item.size)} · {item.kind} · {item.reason||'culled'}</p><p className="dim mono">{item.stored}</p></article>)}{legacyActive.length>0&&<p className="binacts"><button className="btn primary" disabled={busy||legacySelection.size===0} onClick={()=>void previewLegacy()}>Review {legacySelection.size} selected</button></p>}</>}</>}
    </>}
  </>;
}
