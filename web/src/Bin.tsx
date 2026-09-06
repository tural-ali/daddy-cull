import {useEffect,useState} from 'react';
import {get,type Asset,type Page} from './api';

type Plan={id:string;state:string;created:string;assets:{id:number;path:string}[];files:{original:string;size:number;sidecar:boolean;phase:string}[];warnings:string[];error?:string};
async function post<T>(path:string,body:unknown):Promise<T>{
 const r=await fetch(path,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});
 const text=await r.text();let result;try{result=JSON.parse(text)}catch{throw Error('The Bin result could not be confirmed. Refresh the Bin before retrying.')}
 if(!r.ok)throw Error(result.error||'Bin request failed');return result;
}
const size=(n:number)=>`${(n/1048576).toFixed(1)} MB`;
export function Bin({onClose}:{onClose:()=>void}){
 const [marked,setMarked]=useState<Asset[]>([]);const [plans,setPlans]=useState<Plan[]>([]);
 const [selection,setSelection]=useState<Set<number>>(new Set());const [plan,setPlan]=useState<Plan|null>(null);
 const [mode,setMode]=useState<'quarantine'|'restore'|'purge'>('quarantine');const [confirm,setConfirm]=useState('');
 const [busy,setBusy]=useState(false);const [error,setError]=useState('');const [notice,setNotice]=useState('');
 async function refresh(){try{const [m,p]=await Promise.all([get<Page>('/api/assets?view=review&status=cull&limit=200&groups=0'),get<Plan[]>('/api/bin')]);setMarked(m.assets.filter(a=>a.source==='archive'));setPlans(p)}catch(e){setError((e as Error).message)}}
 useEffect(()=>{void refresh()},[]);
 async function preview(){if(!selection.size)return;setBusy(true);setError('');try{setPlan(await post<Plan>('/api/bin/preview',{ids:[...selection]}));setMode('quarantine');setConfirm('')}catch(e){setError((e as Error).message)}finally{setBusy(false)}}
 async function execute(){if(!plan)return;setBusy(true);setError('');try{
  const result=await post<Plan>('/api/bin/execute',{id:plan.id,action:mode,confirmation:confirm});
  setNotice(result.state==='purged_recovered'?'Deletion recovered after interruption. Some files were already absent after recorded deletion intent; see the receipt warnings.':mode==='quarantine'?'Selected files are now in the Bin. You can restore them.':mode==='restore'?'Files restored. They are back in the undecided queue.':'Selected Bin files permanently deleted.');setSelection(new Set());setPlan(null);setPlans(ps=>[result,...ps.filter(p=>p.id!==result.id)]);await refresh();
 }catch(e){setError((e as Error).message);await refresh()}finally{setBusy(false)}}
 function inspect(p:Plan,action:'restore'|'purge'|'quarantine'){setPlan(p);setMode(action);setConfirm('');setError('')}
 const active=plans.filter(p=>p.state!=='restored'&&p.state!=='purged'&&p.state!=='purged_recovered');
 return <section className="bin-workspace"><div className="batchhead"><div><h1>Marked files & Bin</h1><p>Marking is a choice. Moving to the Bin changes where the files live.</p></div><button disabled={busy} onClick={onClose}>Back to review</button></div>
 {error&&<div role="alert" className="error">{error} <button disabled={busy} onClick={()=>{setError('');void refresh()}}>Refresh Bin</button></div>}
 <div className="notice" role="status">{busy?'Verifying files and processing the selected batch. This can take time for large videos…':notice}</div>
 {plan?<div className="bin-confirm"><h2>{mode==='quarantine'?'Review the exact files to move':mode==='restore'?'Restore these files to their original paths':'Permanently delete these Bin files'}</h2><p>{plan.assets.length} media files and {plan.files.filter(f=>f.sidecar).length} sidecars · {size(plan.files.reduce((n,f)=>n+f.size,0))}</p>
 <ul className="file-manifest">{plan.files.map(f=><li key={f.original}><span>{f.original}</span><small>{f.sidecar?'Sidecar':'Media'} · {size(f.size)} · {f.phase}</small></li>)}</ul>
 {plan.warnings.map(w=><p className="warning" key={w}>{w}</p>)}
 {mode==='quarantine'&&<p>Contents were verified with SHA-256 and will be checked again before moving. Takeout sources stay untouched.</p>}
 {mode==='restore'&&<p>Existing files will not be overwritten. A collision stops the batch for review.</p>}
 {mode==='purge'&&<label className="delete-confirm">This cannot be undone. Type <strong>DELETE {plan.files.length}</strong> to delete these media files and sidecars.<input aria-label="Permanent deletion confirmation" value={confirm} onChange={e=>setConfirm(e.target.value)} autoComplete="off"/></label>}
 <div className="compare-actions"><button disabled={busy||(mode==='purge'&&confirm!==`DELETE ${plan.files.length}`)} className={mode==='purge'?'cull':'primary'} onClick={()=>void execute()}>{mode==='quarantine'?`Move ${plan.files.length} files to Bin`:mode==='restore'?`Restore ${plan.files.length} files`:`Permanently delete ${plan.files.length} files`}</button><button disabled={busy} onClick={()=>setPlan(null)}>Cancel</button></div></div>:<>
 <div className="batchhead"><h2>Marked for culling ({marked.length}{marked.length===200?'+':''})</h2><button className="primary" disabled={busy||!selection.size} onClick={()=>void preview()}>Review {selection.size} selected</button></div><p>Select up to 20 archive files per operation. No files move until you approve the preview.</p>
 <div className="marked-list">{marked.map(a=><label key={a.id}><input type="checkbox" checked={selection.has(a.id)} disabled={busy||(!selection.has(a.id)&&selection.size>=20)} onChange={()=>setSelection(s=>{const n=new Set(s);n.has(a.id)?n.delete(a.id):n.add(a.id);return n})}/><span>{a.path}<small>{size(a.size)}</small></span></label>)}{!marked.length&&<p>No archive files are waiting to move.</p>}</div>
 <h2>Bin ({active.length} batches)</h2>{!active.length&&<p>The Bin is empty. Marked photos remain in the archive until moved.</p>}
 {active.map(p=><article className="bin-batch" key={p.id}><h3>{p.assets.length} media · {p.files.length} files including sidecars</h3><p>{new Date(p.created).toLocaleString()} · {p.state}</p><ul>{p.assets.map(a=><li key={a.id}>{a.path}</li>)}</ul>{p.error&&<p className="warning">{p.error}</p>}<div className="compare-actions">{p.state==='bin'?<><button disabled={busy} onClick={()=>inspect(p,'restore')}>Review restore</button><button disabled={busy} className="cull" onClick={()=>inspect(p,'purge')}>Review permanent deletion</button></>:<button disabled={busy} onClick={()=>inspect(p,p.state==='restoring'?'restore':p.state==='purging'?'purge':'quarantine')}>Review interrupted operation</button>}{p.state==='quarantining'&&<button disabled={busy} onClick={()=>inspect(p,'restore')}>Review rollback to archive</button>}</div></article>)}
 <details className="library-note"><summary>Completed operations ({plans.length-active.length})</summary>{plans.filter(p=>p.state==='restored'||p.state==='purged'||p.state==='purged_recovered').map(p=><p key={p.id}>{p.assets.length} media · {p.state} · {new Date(p.created).toLocaleString()} {p.warnings.join(' ')}</p>)}</details>
 </>}
 </section>;
}
