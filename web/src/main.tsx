import {useEffect,useEffectEvent,useRef,useState} from 'react';
import {createRoot} from 'react-dom/client';
import {type Asset,type Page,type Status,decide,get} from './api';
import './style.css';
import {Media} from './Media';
import {Compare} from './Compare';
import {Bin} from './Bin';
import {recoverPending} from './recoverPending';
import {useDecisionQueue} from './useDecisionQueue';

type Undo={asset:Asset;status:Status;favourite:boolean;wasResolved:boolean};
function newRequestId(){const bytes=crypto.getRandomValues(new Uint8Array(16));return Array.from(bytes,b=>b.toString(16).padStart(2,'0')).join('')}
const labels:Record<Status,string>={unreviewed:'Not reviewed',keep:'Keeping',later:'For later',cull:'Marked for culling'};
type Resume={cursor:string;assetId:number;review:boolean;comparison:boolean;kind:string;from:string;status:string;matches:boolean};
function readResume():Partial<Resume>{try{const r=JSON.parse(localStorage.getItem('cull.session.v1')||'{}');const saved=r&&typeof r==='object'?r:{};return new URLSearchParams(location.search).get('view')==='grid'?{...saved,review:false,comparison:false}:saved}catch{return {}}}
// Read once, when the page loads.
const resume=readResume();
function App(){
  const resumeApplied=useRef(false);
  useEffect(()=>{const url=new URL(location.href);if(url.searchParams.get('view')==='grid'){url.searchParams.delete('view');window.history.replaceState(window.history.state,'',url)}},[]);
  const [page,setPage]=useState<Page>({assets:[],next:''});
  const [total,setTotal]=useState<number|null>(null);
  const [source,setSource]=useState('');const [kind,setKind]=useState(resume.kind||'');
  const [from,setFrom]=useState(()=>resume.from??localStorage.getItem('cull.from')??'');
  const [synthetic,setSynthetic]=useState(true);
  const [snapshot,setSnapshot]=useState('');
  const [candidates,setCandidates]=useState(0);
  const [matches,setMatches]=useState(resume.matches||false);
  const [statusFilter,setStatusFilter]=useState(resume.status??'unreviewed');
  const [nextPage,setNextPage]=useState<Page|null>(null);
  const pageCache=useRef<{key:string;page:Page}|null>(null);
  const finishBatch=useRef(false);
  const [comparison,setComparison]=useState<Asset|null>(null);
  const [relatedComparison,setRelatedComparison]=useState(false);
  const [binOpen,setBinOpen]=useState(false);
  const [cursor,setCursor]=useState(resume.cursor||'');const [history,setHistory]=useState<string[]>([]);
  const [at,setAt]=useState(0);const [review,setReview]=useState(resume.review||false);
  const [loading,setLoading]=useState(true);const [busy,setBusy]=useState(false);
  const saving=useRef(false);
  const [error,setError]=useState('');const [notice,setNotice]=useState('');
  const [undo,setUndo]=useState<Undo[]>([]);
  const [readyMedia,setReadyMedia]=useState<Set<number>>(new Set());
  const [resolved,setResolved]=useState<Set<number>>(new Set());
  const [reload,setReload]=useState(0);
  const [recovering,setRecovering]=useState(true);const [recoveryError,setRecoveryError]=useState('');
  async function recover(){setRecovering(true);setRecoveryError('');try{await recoverPending();setReload(x=>x+1)}catch(e){setRecoveryError((e as Error).message)}finally{setRecovering(false)}}
  useEffect(()=>{void recover()},[]);
  const current=page.assets[at];
  useEffect(()=>{const abort=new AbortController();get<{total:number;synthetic:boolean;snapshotAt:string;candidates:number}>('/api/stats',abort.signal).then(s=>{setTotal(s.total);setSynthetic(s.synthetic);setSnapshot(s.snapshotAt);setCandidates(s.candidates)}).catch(e=>{if(!abort.signal.aborted)setError(e.message)});return()=>abort.abort()},[]);
  useEffect(()=>{
    const abort=new AbortController();setError('');setNextPage(null);
    const qs=new URLSearchParams({limit:'40',view:'review',groups:'1',kind,after:cursor,from,matches:matches?'1':'0',status:statusFilter});
    if(pageCache.current?.key===qs.toString()){setPage(pageCache.current.page);setAt(0);setLoading(false);pageCache.current=null;return()=>abort.abort()}
    setLoading(true);get<Page>('/api/assets?'+qs,abort.signal).then(p=>{setPage(p);let index=0;if(!resumeApplied.current){index=Math.max(0,p.assets.findIndex(a=>a.id===resume.assetId));resumeApplied.current=true;if(resume.comparison&&(p.assets[index]?.relatedCount??0)>0){setRelatedComparison(true);setComparison(p.assets[index])}}setAt(index);setLoading(false)}).catch(e=>{if(!abort.signal.aborted){setError(e.message);setLoading(false)}});
    return()=>abort.abort();
  },[source,kind,cursor,reload,from,matches,statusFilter]);
  useEffect(()=>{
    if(!page.next)return;
    const abort=new AbortController();
    const qs=new URLSearchParams({limit:'40',view:'review',groups:'1',kind,after:page.next,from,matches:matches?'1':'0',status:statusFilter});
    get<Page>('/api/assets?'+qs,abort.signal).then(p=>{pageCache.current={key:qs.toString(),page:p};setNextPage(p)}).catch(()=>{});
    return()=>abort.abort();
  },[page.next,kind,from,matches,statusFilter]);
  const remember=useEffectEvent(()=>{if(!current)return;try{localStorage.setItem('cull.session.v1',JSON.stringify({cursor,assetId:comparison?.id??current.id,review,comparison:!!comparison,kind,from,status:statusFilter,matches}))}catch{}});
  useEffect(()=>{if(!loading)remember()},[loading,current?.id,cursor,review,comparison,kind,from,statusFilter,matches]);
  const openRelated=useEffectEvent(()=>{if(review&&!comparison&&current&&(current.relatedCount??0)>0){setRelatedComparison(true);setComparison(current)}});
  useEffect(()=>{if(!loading)openRelated()},[review,current?.id,loading]);
  function nextMemory(){
    const i=page.assets.findIndex(a=>a.id===comparison?.id);const next=page.assets[i+1];
    setComparison(null);setReview(true);
    if(next){setAt(i+1);if((next.relatedCount??0)>0){setRelatedComparison(true);setComparison(next)}}
    else{setCursor(page.next||'');setReload(x=>x+1)}
  }
  function filter(which:'source'|'kind',value:string){if(saving.current)return;setCursor('');setHistory([]);setReview(false);if(which==='source')setSource(value);else setKind(value)}
  function patch(id:number,data:Partial<Asset>){setPage(p=>({...p,assets:p.assets.map(a=>a.id===id?{...a,...data}:a)}))}
  const queue=useDecisionQueue((job,r)=>{
    const {asset,status,favourite,wasResolved}=job;
    const updated={...asset,status,favourite,revision:r.revision};patch(asset.id,updated);
    if(!review&&statusFilter==='unreviewed'&&status!=='unreviewed')setPage(p=>({...p,assets:p.assets.filter(a=>a.id!==asset.id)}));
    setUndo(u=>[...u.slice(-19),{asset:updated,status:r.previousStatus,favourite:r.previousFavourite,wasResolved}]);
    setResolved(s=>{const n=new Set(s);if(status==='keep'||status==='cull')n.add(asset.id);else n.delete(asset.id);return n});
    setNotice(status==='cull'?'Marked for culling. No file was moved.':'Decision saved.');
  });
  const position=useRef(at);position.current=at;
  const locked=busy||queue.pending>0||recovering||!!recoveryError;
  function save(status:Status,favourite?:boolean){
    const index=position.current;const asset=page.assets[index];
    if(!asset||saving.current||loading||recovering||recoveryError||!readyMedia.has(asset.id))return;
    if(!queue.enqueue({asset,status,favourite:favourite??asset.favourite,wasResolved:resolved.has(asset.id)}))return;
    if(favourite===undefined){
      if(index<page.assets.length-1){position.current=index+1;setAt(index+1)}
      else if(page.next){setHistory(h=>[...h.slice(-99),cursor]);setCursor(page.next);position.current=0;if(nextPage){setPage(nextPage);setAt(0)}else setLoading(true)}
      else{finishBatch.current=true;setAt(page.assets.length)}
    }
  }
  useEffect(()=>{if(finishBatch.current&&queue.pending===0){finishBatch.current=false;setCursor('');setHistory([]);setReload(x=>x+1)}},[queue.pending]);
  async function undoLast(){
    const last=undo.at(-1);if(!last||saving.current||queue.pending)return;
    saving.current=true;setBusy(true);setError('');
    try{
      const r=await decide(last.asset,last.status,last.favourite,newRequestId());
      patch(last.asset.id,{status:last.status,favourite:last.favourite,revision:r.revision});
      setUndo(u=>u.slice(0,-1).map(x=>x.asset.id===last.asset.id?{...x,asset:{...x.asset,revision:r.revision}}:x));
      setResolved(s=>{const n=new Set(s);if(last.wasResolved)n.add(last.asset.id);else n.delete(last.asset.id);return n});setNotice('Previous decision restored.');
    }catch(e){setError((e as Error).message)}finally{saving.current=false;setBusy(false)}
  }
  useEffect(()=>{
    function key(e:KeyboardEvent){
      if(binOpen||comparison||!review||!current||busy||loading||e.ctrlKey||e.metaKey||e.altKey||e.target instanceof HTMLInputElement||e.target instanceof HTMLSelectElement)return;
      if(e.key==='Escape'){setReview(false);return}
      if(e.key==='ArrowRight'){e.preventDefault();setAt(i=>Math.min(i+1,page.assets.length-1))}
      if(e.key==='ArrowLeft'){e.preventDefault();setAt(i=>Math.max(0,i-1))}
      if(e.repeat)return;
      if(['k','x','l','f','u'].includes(e.key.toLowerCase())){e.preventDefault();switch(e.key.toLowerCase()){case 'k':save('keep');break;case 'x':save('cull');break;case 'l':save('later');break;case 'f':save(current.status,!current.favourite);break;case 'u':void undoLast()}}
    }
    window.addEventListener('keydown',key);return()=>window.removeEventListener('keydown',key);
  });
  return <div className={review||comparison?"app reviewing":"app"}>
    <header><a className="brand" href="/queue.html?view=grid">Daddy, Cull!<span>Make room for the memories.</span></a><div><button disabled={locked} onClick={()=>{setBinOpen(true);setReview(false)}}>Marked files & Bin</button></div></header>
    <div className="prototype">{synthetic?"Sample catalogue":"Your real family archive"} · Review choices save first. Files move only through the Bin workflow.</div>
    <main>
      {recovering?<div className="empty">Recovering any pending choices…</div>:recoveryError?<div className="error" role="alert">{recoveryError} <button onClick={()=>void recover()}>Retry recovery</button></div>:binOpen?<Bin onClose={()=>{setBinOpen(false);setCursor('');setReload(x=>x+1)}}/>:<>
      <section className="intro"><div><h1>{review?'One memory at a time.':'A small session. A lighter library.'}</h1><p>{review?'Keep what matters. Leave the difficult choices for later.':'Choose a date. Twenty decisions is a good place to stop.'}</p></div>
      <div className="session"><span>{resolved.size} / 20 decisions this session</span><progress max="20" value={Math.min(20,resolved.size)}/>{resolved.size>=20&&<strong>A good stopping point. Your decisions are saved.</strong>}</div></section>
      <div className="toolbar"><label>Start from<input aria-label="Start from date" type="date" value={from} disabled={locked} onChange={e=>{setFrom(e.target.value);localStorage.setItem('cull.from',e.target.value);setCursor('');setHistory([]);setReview(false)}}/></label><label>Show<select aria-label="Review status" value={statusFilter} disabled={locked} onChange={e=>{setStatusFilter(e.target.value);setCursor('');setHistory([]);setReview(false)}}><option value="unreviewed">Undecided</option><option value="later">For later</option><option value="keep">Keeping</option><option value="cull">Marked for culling</option><option value="">All decisions</option></select></label><label>Media<select value={kind} disabled={locked} onChange={e=>filter('kind',e.target.value)}><option value="">All media</option><option value="image">Photos</option><option value="raw">RAW</option><option value="video">Videos</option></select></label><button disabled={locked} className={matches?"primary":""} onClick={()=>{setMatches(!matches);setStatusFilter(matches?'unreviewed':'');setCursor('');setHistory([]);setFrom('');setReview(false)}}>{matches?'Show all memories':`Possible upgrades (${candidates})`}</button><span className="total">{total===null?'Loading catalogue…':`${total.toLocaleString()} assets`}</span><button disabled={!undo.length||locked||loading} onClick={()=>void undoLast()}>Undo <kbd>U</kbd></button></div>
      {!synthetic&&<details className="library-note"><summary>Library snapshot and Takeout status</summary><p>Archive snapshot: {snapshot || 'just imported'} UTC. Existing favourites are preserved. Review choices here are separate from the old app.</p><p>{candidates} available Takeout candidates from the existing report are linked to their archive files. Missing candidates are excluded. The full Takeout collection has not yet been scanned.</p></details>}
      {error&&<div role="alert" className="error">{error} <button disabled={locked} onClick={()=>{setError('');setReload(x=>x+1)}}>Reload catalogue</button></div>}
      {queue.error&&<div role="alert" className="error">{queue.error} <button onClick={queue.retry}>Retry pending saves</button></div>}
      <div className="notice" role="status" aria-live="polite">{busy?'Saving…':queue.pending?`${queue.pending} choice${queue.pending===1?'':'s'} saving…`:notice}</div>
      {comparison?<Compare related={relatedComparison} asset={comparison} onNext={nextMemory} onClose={()=>{setComparison(null);setReview(false);setReload(x=>x+1)}} onChanged={changes=>{setUndo([]);for(const file of changes)patch(file.id,file);setResolved(previous=>{const next=new Set(previous);for(const file of changes){if(file.status==='keep'||file.status==='cull')next.add(file.id);else next.delete(file.id)}return next})}}/>:loading?<div className="empty">Opening your next batch…</div>:!current?<div className="empty"><h2>{queue.pending?'Finishing your saved choices…':statusFilter==='unreviewed'?'No undecided photos in this view.':'No photos in this view.'}</h2><p>{queue.pending?'Your pending choices are retained locally.':'Change the date or media filter, or revisit your choices using Show.'}</p></div>:review?<>
        <section className="review"><div className="stage">{[...page.assets.slice(Math.max(0,at-1),at+5),...(at+5>=page.assets.length?(nextPage?.assets.slice(0,4)??[]):[])].map(asset=><div className="review-frame" key={asset.id} hidden={asset.id!==current.id}><Media asset={asset} large onReady={id=>setReadyMedia(s=>s.has(id)?s:new Set(s).add(id))}/></div>)}</div><aside className="details"><span className="source">{current.source==='takeout'?'Google Takeout':'Family archive'}</span><h2>{current.path.split('/').pop()}</h2><p>{current.capturedAt?new Date(current.capturedAt*1000).toLocaleString():'Date unknown'}</p><dl><dt>Decision</dt><dd>{labels[current.status]}</dd><dt>File size</dt><dd>{(current.size/1048576).toFixed(1)} MB</dd><dt>Favourite</dt><dd>{current.favourite?'Yes':'Not yet'}</dd></dl><p className="path">{current.path}</p>{current.alternativeCount>0&&<button disabled={locked} className="primary" onClick={()=>{setRelatedComparison(false);setComparison(current)}}>Compare possible upgrade ({current.alternativeCount})</button>}{(current.relatedCount??0)>0&&<button disabled={locked} className="primary" onClick={()=>{setRelatedComparison(true);setComparison(current)}}>Compare related copies ({(current.relatedCount??0)+1})</button>}<p className="helper">Marking for culling saves your choice. It does not move or delete a file.</p><button disabled={busy||!queue.ready||!!queue.error||!readyMedia.has(current.id)} onClick={()=>save(current.status,!current.favourite)}>{current.favourite?'★ Favourite':'☆ Favourite'} <kbd>F</kbd></button></aside></section>
        <div className="decisionbar"><button disabled={busy||at===0} aria-label="Previous asset" onClick={()=>setAt(at-1)}>←</button><span>{at+1} of {page.assets.length}</span><button disabled={busy||at===page.assets.length-1} aria-label="Next asset" onClick={()=>setAt(at+1)}>→</button><div className="choices"><button disabled={busy||!queue.ready||!!queue.error||!readyMedia.has(current.id)} className="primary" onClick={()=>save('keep')}>Keep <kbd>K</kbd></button><button disabled={busy||!queue.ready||!!queue.error||!readyMedia.has(current.id)} onClick={()=>save('later')}>Later <kbd>L</kbd></button><button disabled={busy||!queue.ready||!!queue.error||!readyMedia.has(current.id)} className="cull" onClick={()=>save('cull')}>Mark for culling <kbd>X</kbd></button></div><button disabled={locked} onClick={()=>setReview(false)}>Back to library</button></div>
      </>:<><div className="batchhead"><h2>Your next {page.assets.length} memories</h2><button className="primary" onClick={()=>{setAt(Math.max(0,page.assets.findIndex(a=>a.status==='unreviewed')));if((page.assets[Math.max(0,page.assets.findIndex(a=>a.status==='unreviewed'))]?.relatedCount??0)>0){setRelatedComparison(true);setComparison(page.assets[Math.max(0,page.assets.findIndex(a=>a.status==='unreviewed'))])}setReview(true)}}>Start reviewing</button></div><div className="grid">{page.assets.map((a,i)=><button className={`tile ${a.status}`} key={a.id} onClick={()=>{setAt(i);if((a.relatedCount??0)>0){setRelatedComparison(true);setComparison(a)}else setReview(true)}}><div className={`mini ${a.kind}`}><Media key={a.id} asset={a}/>{/* favourite overlay */}{a.favourite&&<b>★</b>}</div><div className="tiletext"><strong>{a.path.split('/').pop()}</strong><span>{a.capturedAt?new Date(a.capturedAt*1000).toLocaleDateString(undefined,{year:'numeric',month:'short',day:'numeric'}):'Date unknown'}</span><small>{(a.relatedCount??0)>0?`Possible duplicate / related copy · ${(a.relatedCount??0)+1} files`:a.alternativeCount>0?`Possible upgrade · ${a.alternativeCount} alternative`:labels[a.status]}</small></div></button>)}</div></>}
      <footer><p>Browsing is not a decision. Every saved choice can be changed.</p><div><button disabled={locked||loading||history.length===0} onClick={()=>{setCursor(history.at(-1)!);setHistory(h=>h.slice(0,-1))}}>Previous batch</button><button disabled={locked||loading||!page.next} onClick={()=>{setHistory(h=>[...h.slice(-99),cursor]);setCursor(page.next)}}>Next batch</button></div></footer>
    </>}
    </main>
  </div>;
}
createRoot(document.getElementById('root')!).render(<App/>);
