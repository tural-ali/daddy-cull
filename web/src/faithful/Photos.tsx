import {useCallback,useEffect,useRef,useState} from 'react';
import {FilePreview} from '../Media';
import {Busy} from '../Busy';
import {CullSyncSetup} from './CullSyncSetup';

type JobState='queued_check'|'checking'|'planned'|'queued_apply'|'applying'|'done'|'failed'|'cancelled';
type Agent={online:boolean;lastSeen?:string;version?:string;access?:string};
type Summary={id:string;state:JobState;rev:number;stage?:string;message?:string;done:number;total:number;error?:string};
type Status={configured:boolean;settling?:boolean;now:string;agent:Agent;job:Summary|null};
type PhotosAsset={id:string;name:string;created:string;favourite:boolean;thumb?:string};
type Row={id:string;action:'delete'|'favourite';keys:string[];name:string;day:string;original:string;kind:string;ext:string;state?:'marked'|'bin'|'purged';preview?:string;how:'exact'|'near';photos:PhotosAsset[];outcome?:'deleted'|'not-deleted'|'favourited'|'failed';outcomeError?:string};
type Held={name:string;day:string;kept:string};
type Result={nothing?:boolean;deleted:number;notDeleted:number;favourited:number;favouriteFailed:number;note?:string};
type Job=Summary&{created:string;updated:string;toCheck:number;delete:Row[];favourite:Row[];missing:{action:string;name:string;day:string}[];held:Held[];undated:number;selected:string[];skipped:number;result?:Result};
type Restored={key:string;name:string;day:string;syncedAt:string};
type Overview={delete:number;favourite:number;held:number;undated:number;restored:Restored[];synced:{deleted:number;favourited:number;last:string}};

const ACTIVE:JobState[]=['queued_check','checking','queued_apply','applying'];
const CANCELLABLE:JobState[]=['queued_check','checking','planned','queued_apply'];

async function read<T>(url:string):Promise<T>{
  const response=await fetch(url,{cache:'no-store'});
  if(!response.ok)throw new Error('The Photos sync could not be read. Check that the local service is running.');
  return response.json() as Promise<T>;
}
// send posts a page action. A refusal that carries the job back (a sync already
// running, a plan that went stale) still hands the page the job, so it shows
// where things stand rather than only an error.
async function send(path:string,body:unknown):Promise<{job?:Job;error?:string;forgotten?:number}>{
  const response=await fetch(path,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});
  let result:{error?:string;job?:Job;forgotten?:number}&Partial<Job>={};
  try{result=await response.json()}catch{throw new Error('The server did not confirm what happened. Reload the page to see where things stand.')}
  if(response.ok)return 'id' in result?{job:result as Job}:result;
  return {job:result.job,error:result.error||'The request failed.'};
}

function plural(count:number,one:string,many=`${one}s`){return `${count.toLocaleString()} ${count===1?one:many}`}
function day(value:string){
  if(!value)return 'undated';
  const at=new Date(`${value}T12:00:00Z`);
  return Number.isNaN(at.getTime())?value:at.toLocaleDateString('en-GB',{day:'numeric',month:'short',year:'numeric',timeZone:'UTC'});
}
function when(value:string){
  if(!value)return 'never';
  const at=new Date(/[zZ]|[+-]\d\d:\d\d$/.test(value)?value:`${value}Z`);
  return Number.isNaN(at.getTime())?value:at.toLocaleString('en-GB',{day:'numeric',month:'short',hour:'2-digit',minute:'2-digit'});
}
// ago measures against the server's clock, so a Mac and a browser that disagree
// about the time cannot make a live helper look an hour stale.
function ago(value:string|undefined,now:string){
  if(!value)return '';
  const seconds=Math.max(0,Math.round((Date.parse(now)-Date.parse(value))/1000));
  if(!Number.isFinite(seconds))return '';
  if(seconds<10)return 'just now';
  if(seconds<90)return `${seconds} seconds ago`;
  if(seconds<5400)return `${Math.round(seconds/60)} minutes ago`;
  if(seconds<129600)return `${Math.round(seconds/3600)} hours ago`;
  return `on ${when(value)}`;
}

function progress(job:Summary,deletes:number):{label:string;state:'connecting'|'searching'|'working'|'listening'|'solving'}{
  if(job.state==='queued_check')return {label:'Waiting for Cull Sync on the Mac to pick this up…',state:'connecting'};
  if(job.state==='queued_apply')return {label:'Waiting for Cull Sync on the Mac to pick up the changes…',state:'connecting'};
  if(job.state==='checking'){
    if(job.stage==='matching')return {label:'Finding each photograph in Photos…',state:'searching'};
    if(job.stage==='thumbnails')return {label:'Fetching previews from Photos…',state:'searching'};
    return {label:'Reading the Photos library on the Mac…',state:'searching'};
  }
  if(job.stage==='favourites')return {label:'Setting favourites in Photos…',state:'working'};
  if(job.stage==='confirm')return {label:`Confirm on your Mac: Photos is asking before it moves ${plural(deletes,'item')} to Recently Deleted.`,state:'listening'};
  if(job.stage==='verifying')return {label:'Checking that Photos really made each change…',state:'solving'};
  return {label:'Starting on the Mac…',state:'working'};
}

function access(agent:Agent){
  switch(agent.access){
    case 'limited':return 'Cull Sync can see only some of the Photos library. Give it full access in System Settings, Privacy & Security, Photos, or it will miss photographs.';
    case 'denied':
    case 'restricted':return 'Cull Sync is not allowed to use Photos. Allow it in System Settings, Privacy & Security, Photos.';
    case 'notDetermined':return 'Cull Sync is waiting for Photos access. Answer the prompt on the Mac.';
    default:return '';
  }
}

// setupIntro opens the setup dialog with where things stand, so it reads as an
// answer to what the reviewer is seeing rather than as a generic manual.
function setupIntro(status:Status){
  const agent=status.agent;
  if(!status.configured)return 'Cull Sync is the small app on your Mac that makes the changes in Photos. It is not set up yet. Set it up once, on the Mac that has your Photos library, and it keeps running from then on, starting again whenever you log in.';
  if(agent.online)return 'Cull Sync is running. Set it up again only to update it or to move it to another Mac: whichever Mac runs this command takes over, and the key the old one has stops working.';
  if(agent.lastSeen)return `Cull Sync last answered ${ago(agent.lastSeen,status.now)}. If that Mac is asleep, wake it and this page will notice. If Cull Sync was removed or keeps failing, set it up again.`;
  return 'Cull Sync has not called in since the server started. If it is installed, make sure that Mac is awake and logged in. Otherwise, set it up now.';
}

function where(row:Row){
  if(row.action==='favourite')return 'Favourite in Cull';
  if(row.state==='purged')return 'Deleted from the archive for good';
  if(row.state==='bin')return 'In the Bin';
  return 'Marked to cull';
}
const OUTCOME:Record<NonNullable<Row['outcome']>,string>={
  'deleted':'Moved to Recently Deleted',
  'not-deleted':'Still in Photos',
  'favourited':'Favourite set',
  'failed':'Favourite not set',
};

function PhotosRow({row,chosen,editable,onToggle}:{row:Row;chosen:boolean;editable:boolean;onToggle:()=>void}){
  const body=<>
    <div className="pthumb archive" title={row.original}>
      {row.preview?<FilePreview base={row.preview} name={row.name} kind={row.kind}/>
        :<div className="media-missing"><span>No preview</span><small>{row.ext.toUpperCase()} · gone from the archive</small></div>}
    </div>
    <span className="parrow" aria-hidden="true">→</span>
    <div className="pmatches">
      {row.photos.map(asset=><figure key={asset.id} className="pthumb" title={asset.name}>
        {asset.thumb?<img src={asset.thumb} alt={`${asset.name} in Photos`} loading="lazy" decoding="async"/>
          :<div className="media-missing"><span>No preview</span></div>}
        {asset.favourite&&<span className="pfav" aria-label="Already a favourite in Photos">♥</span>}
      </figure>)}
    </div>
    <div className="pinfo">
      <span className="mono" title={row.original}>{row.name}</span>
      <span className="dim">{day(row.day)} · {where(row)}</span>
      <span className="dim">
        <span className={`pbadge ${row.how}`}>{row.how==='near'?'A day apart':'Same name and day'}</span>
        {row.photos.length>1&&` · ${row.photos.length} copies in Photos, all included`}
        {row.photos.length===1&&row.photos[0].name&&row.photos[0].name!==row.name&&` · called ${row.photos[0].name} in Photos`}
      </span>
      {row.outcome&&<span className={`pout ${row.outcome}`}>{OUTCOME[row.outcome]}{row.outcomeError&&`: ${row.outcomeError}`}</span>}
    </div>
  </>;
  if(!editable)return <li className={`prow${row.outcome?` ${row.outcome}`:''}`}>{body}</li>;
  return <li className={`prow${chosen?' sel':''}`}>
    <label>
      <input type="checkbox" checked={chosen} onChange={onToggle} aria-label={`Include ${row.name}`}/>
      {body}
    </label>
  </li>;
}

function RowGroup({title,hint,rows,chosen,editable,onToggle,onAll}:{title:string;hint:string;rows:Row[];chosen:Set<string>;editable:boolean;onToggle:(id:string)=>void;onAll:(ids:string[],on:boolean)=>void}){
  if(rows.length===0)return null;
  const picked=rows.filter(row=>chosen.has(row.id)).length;
  return <section className="psec">
    <header>
      <h2>{title} <span className="dim">{rows.length.toLocaleString()}</span></h2>
      {editable&&<label className="selall"><input type="checkbox" checked={picked===rows.length} ref={input=>{if(input)input.indeterminate=picked>0&&picked<rows.length}} onChange={()=>onAll(rows.map(row=>row.id),picked!==rows.length)}/> All</label>}
    </header>
    <p className="hint">{hint}</p>
    <ul className="plist">{rows.map(row=><PhotosRow key={row.id} row={row} chosen={chosen.has(row.id)} editable={editable} onToggle={()=>onToggle(row.id)}/>)}</ul>
  </section>;
}

export function Photos(){
  const [status,setStatus]=useState<Status|null>(null);
  const [job,setJob]=useState<Job|null>(null);
  const [overview,setOverview]=useState<Overview|null>(null);
  const [chosen,setChosen]=useState<Set<string>>(new Set());
  const [posting,setPosting]=useState('');
  const [error,setError]=useState('');
  const [offline,setOffline]=useState('');
  const [setup,setSetup]=useState<{auto:boolean;intro:string}|null>(null);
  const [connected,setConnected]=useState(false);
  const offered=useRef(false);
  const current=useRef<Status|null>(null);
  const selectionFor=useRef('');
  const lastState=useRef<JobState|''>('');
  const timer=useRef(0);

  // poll reads the light status on a short cycle while the Mac is working and a
  // slow one otherwise. It is also called straight after every action, so a
  // click is answered at once rather than at the next tick.
  const poll=useCallback(async()=>{
    window.clearTimeout(timer.current);
    try{
      const next=await read<Status>('/api/photos');
      current.current=next;setStatus(next);setOffline('');
    }catch(reason){setOffline((reason as Error).message)}
    const busy=current.current?.job&&ACTIVE.includes(current.current.job.state);
    timer.current=window.setTimeout(()=>{void poll()},busy?1500:5000);
  },[]);
  const loadOverview=useCallback(()=>{read<Overview>('/api/photos/overview').then(setOverview).catch(reason=>setError((reason as Error).message))},[]);
  useEffect(()=>{
    void poll();loadOverview();
    return()=>window.clearTimeout(timer.current);
  },[poll,loadOverview]);

  // Without a helper this page can do nothing, so the setup dialog opens by
  // itself, once per visit. Not while the server has only just started: a
  // helper that is running may simply not have called in yet.
  useEffect(()=>{
    if(!status||status.settling||offered.current)return;
    offered.current=true;
    if(!status.configured||!status.agent.online)setSetup({auto:true,intro:setupIntro(status)});
  },[status]);
  const openSetup=()=>{if(status){setConnected(false);setSetup({auto:false,intro:setupIntro(status)})}};
  const closeSetup=useCallback((ok:boolean)=>{setSetup(null);setConnected(ok);if(ok)void poll()},[poll]);

  // The full job is read only when it has really changed; progress counts ride
  // on the light status instead.
  const jobKey=status?.job?`${status.job.id}:${status.job.rev}`:'';
  useEffect(()=>{
    if(!jobKey){setJob(null);return}
    let live=true;
    read<Job>(`/api/photos/jobs/${jobKey.split(':')[0]}`).then(next=>{if(live)setJob(next)}).catch(()=>{});
    return()=>{live=false};
  },[jobKey]);

  // A finished sync changes what is outstanding, so the counts are read again.
  const state=status?.job?.state??'';
  useEffect(()=>{
    const was=lastState.current;
    lastState.current=state;
    if(was&&was!==state&&(state==='done'||state==='failed'||state==='cancelled'))loadOverview();
  },[state,loadOverview]);

  // A fresh plan starts with everything ticked: the check already left out
  // whatever the archive still holds, so each row is a real change to make.
  useEffect(()=>{
    if(!job||job.state!=='planned'||selectionFor.current===job.id)return;
    selectionFor.current=job.id;
    setChosen(new Set([...job.delete,...job.favourite].map(row=>row.id)));
  },[job]);

  async function act(label:string,path:string,body:unknown,after?:(result:Awaited<ReturnType<typeof send>>)=>void){
    setPosting(label);setError('');
    try{
      const result=await send(path,body);
      if(result.job)setJob(result.job);
      if(result.error)setError(result.error);
      after?.(result);
    }catch(reason){setError((reason as Error).message)}
    finally{setPosting('');await poll()}
  }
  const check=()=>act('Starting a check…','/api/photos/check',{});
  const cancel=()=>job&&act('Cancelling…','/api/photos/cancel',{job:job.id});
  // Apply is pressed from the bar at the foot of a long list, but what follows
  // happens at the top: the progress, and above all the prompt to confirm on
  // the Mac. The page goes there so that prompt is not missed.
  const apply=()=>job&&act('Sending to the Mac…','/api/photos/apply',{
    job:job.id,
    delete:job.delete.filter(row=>chosen.has(row.id)).map(row=>row.id),
    favourite:job.favourite.filter(row=>chosen.has(row.id)).map(row=>row.id),
  },result=>{if(!result.error)window.scrollTo({top:0,behavior:'smooth'})});
  const forget=()=>overview&&act('Clearing the note…','/api/photos/forget',{keys:overview.restored.map(item=>item.key)},()=>loadOverview());

  function toggle(id:string){setChosen(set=>{const next=new Set(set);if(next.has(id))next.delete(id);else next.add(id);return next})}
  function all(ids:string[],on:boolean){setChosen(set=>{const next=new Set(set);for(const id of ids){if(on)next.add(id);else next.delete(id)}return next})}

  const summary=status?.job??null;
  const view=job&&summary&&job.id===summary.id?job:null;
  const active=!!summary&&ACTIVE.includes(summary.state);
  const planned=view?.state==='planned';
  const showRows=!!view&&(view.state==='planned'||view.state==='queued_apply'||view.state==='applying'||view.state==='done'||(view.state==='failed'&&view.selected.length>0));
  const rows=(list:Row[])=>planned||!view?list:list.filter(row=>view.selected.includes(row.id));
  const deletes=view?view.delete.filter(row=>chosen.has(row.id)).length:0;
  const favourites=view?view.favourite.filter(row=>chosen.has(row.id)).length:0;
  const selectedDeletes=view?view.selected.filter(id=>id.startsWith('delete:')).length:0;
  const agent=status?.agent;
  const warning=agent?access(agent):'';
  const step=summary&&active?progress(summary,selectedDeletes):null;
  const restored=overview?.restored??[];

  return <>
    <section className="binhead photoshead">
      <h1>Apple Photos</h1>
      <p className="ysum">{overview===null?'Reading the catalogue…':<>
        <b>{overview.delete.toLocaleString()}</b> to delete from Photos · <b>{overview.favourite.toLocaleString()}</b> favourite{overview.favourite===1?'':'s'} to set
        {overview.synced.last&&<> · last synced {when(overview.synced.last)}</>}
      </>}</p>
      <p className="hint">What leaves the archive here should leave Photos too, and what is a favourite here should be one there. Cull Sync on the Mac finds each photograph in Photos, shows it to you below, and changes nothing until you apply. Deletions go to Recently Deleted, and Photos asks on the Mac first.</p>
    </section>

    {offline&&<p className="note warn" role="alert">{offline}</p>}
    {connected&&agent?.online&&<p className="note ok" role="status">Cull Sync is connected. Photos can be checked now.</p>}

    {status&&agent&&<div className={`phelper${agent.online?' online':''}`}>
      <span className="pdot" aria-hidden="true"/>
      <span>{!status.configured?<>Cull Sync is not set up yet, so Photos cannot be checked.</>
        :agent.online?<>Cull Sync is running on the Mac{agent.version&&<span className="dim"> · version {agent.version}</span>}</>
        :agent.lastSeen?<>Cull Sync is not answering. Last seen {ago(agent.lastSeen,status.now)}. Is the Mac awake?</>
        :status.settling?<>Waiting for Cull Sync to call in…</>
        :<>Cull Sync has not connected yet.</>}</span>
      <button type="button" className="btn small" onClick={openSetup}>Set up Cull Sync</button>
    </div>}
    {setup&&<CullSyncSetup intro={setup.intro} auto={setup.auto} agent={agent} onClose={closeSetup}/>}
    {warning&&<p className="note warn" role="alert">{warning}</p>}

    {restored.length>0&&<div className="note warn" role="alert">
      <b>{plural(restored.length,'photograph')} deleted from Photos {restored.length===1?'has':'have'} since been put back in Cull.</b> Photos keeps deleted items in Recently Deleted for about 30 days: open Photos, choose Recently Deleted, select {restored.length===1?'it':'them'} and choose Recover.
      <details className="pmore"><summary>Show which</summary><ul className="plain">{restored.slice(0,200).map(item=><li key={item.key}><span className="mono">{item.name}</span> · {day(item.day)} · deleted from Photos {when(item.syncedAt)}</li>)}</ul>{restored.length>200&&<p>and {(restored.length-200).toLocaleString()} more.</p>}</details>
      <button className="btn small" disabled={!!posting} onClick={()=>void forget()}>I have recovered {restored.length===1?'it':'them'}</button>
    </div>}

    <div className="pacts">
      <button className="btn primary" disabled={!status?.configured||active||!!posting} onClick={()=>void check()}>{planned?'Check again':'Check Photos'}</button>
      {summary&&CANCELLABLE.includes(summary.state)&&<button className="btn" disabled={!!posting} onClick={()=>void cancel()}>{summary.state==='planned'?'Discard this check':'Cancel'}</button>}
      {posting&&<Busy label={posting} state="working"/>}
    </div>
    {error&&<p className="note warn" role="alert">{error}</p>}

    {step&&summary&&<div className={`pprogress${summary.stage==='confirm'?' confirm':''}`}>
      <Busy label={step.label} state={step.state}/>
      {summary.total>0&&<><progress max={summary.total} value={Math.min(summary.done,summary.total)}/><span className="dim">{summary.done.toLocaleString()} of {summary.total.toLocaleString()}</span></>}
      {summary.message&&<span className="dim">{summary.message}</span>}
      {ACTIVE.includes(summary.state)&&agent&&!agent.online&&agent.lastSeen&&<span className="dim">Cull Sync is not answering, so this waits until the Mac is awake and the helper is running.</span>}
    </div>}

    {view?.state==='failed'&&<p className="note warn" role="alert">{view.error||'The sync failed.'}</p>}
    {view?.state==='cancelled'&&<p className="note">The check was cancelled. Nothing was changed in Photos.</p>}
    {view?.state==='done'&&view.result&&(view.result.nothing
      ?<p className="flash" role="status">Photos is up to date. Nothing in Cull needs changing there.</p>
      :<p className="flash" role="status">Photos updated: {plural(view.result.deleted,'photograph')} moved to Recently Deleted and {plural(view.result.favourited,'favourite')} set.</p>)}
    {view?.state==='done'&&view.result&&(view.result.notDeleted>0||view.result.favouriteFailed>0||view.result.note)&&<p className="note warn" role="alert">
      {view.result.notDeleted>0&&<>{plural(view.result.notDeleted,'photograph')} {view.result.notDeleted===1?'is':'are'} still in Photos, because the request on the Mac was declined or Photos did not carry it out. {view.result.notDeleted===1?'It':'They'} will be offered again next time. </>}
      {view.result.favouriteFailed>0&&<>{plural(view.result.favouriteFailed,'favourite')} could not be set. </>}
      {view.result.note}
    </p>}
    {view&&view.skipped>0&&view.state!=='planned'&&<p className="note">{plural(view.skipped,'chosen item')} changed in Cull after the check and {view.skipped===1?'was':'were'} left out.</p>}

    {view&&showRows&&<>
      {planned&&view.delete.length+view.favourite.length===0&&<p className="note">None of what Cull would change is in Photos, so there is nothing to apply.</p>}
      <RowGroup title="Delete from Photos" hint="Left: the file in the archive. Right: what Cull Sync found in Photos. Each goes to Recently Deleted." rows={rows(view.delete)} chosen={chosen} editable={planned} onToggle={toggle} onAll={all}/>
      <RowGroup title="Mark as favourite in Photos" hint="Favourites in Cull that Photos has not been given yet. A heart on the right means Photos already has it as a favourite." rows={rows(view.favourite)} chosen={chosen} editable={planned} onToggle={toggle} onAll={all}/>
      {(view.missing.length>0||view.held.length>0||view.undated>0)&&<section className="psec quiet">
        {view.missing.length>0&&<details className="pmore"><summary>{plural(view.missing.length,'file')} not found in Photos</summary><p className="hint">Nothing to do for these: Photos has no photograph of that name on that day.</p><ul className="plain">{view.missing.slice(0,300).map((item,index)=><li key={index}><span className="mono">{item.name}</span> · {day(item.day)}{item.action==='favourite'&&' · favourite'}</li>)}</ul>{view.missing.length>300&&<p>and {(view.missing.length-300).toLocaleString()} more.</p>}</details>}
        {view.held.length>0&&<details className="pmore"><summary>{plural(view.held.length,'removal')} kept in Photos because the archive still holds another copy</summary><ul className="plain">{view.held.slice(0,300).map((item,index)=><li key={index}><span className="mono">{item.name}</span> · {day(item.day)} · kept as <span className="mono">{item.kept}</span></li>)}</ul>{view.held.length>300&&<p>and {(view.held.length-300).toLocaleString()} more.</p>}</details>}
        {view.undated>0&&<p className="hint">{plural(view.undated,'removed file')} {view.undated===1?'has':'have'} no date to match on and {view.undated===1?'is':'are'} left alone.</p>}
      </section>}
    </>}

    {planned&&view&&view.delete.length+view.favourite.length>0&&<footer className="fbar">
      <span className="fleft">
        <span className="sel">{plural(deletes,'deletion')} · {plural(favourites,'favourite')}</span>
        <button className="btn primary" disabled={!!posting||deletes+favourites===0} onClick={()=>void apply()}>Apply in Photos</button>
      </span>
      <span className="fright hint">Favourites are set first. Photos then asks on the Mac before it deletes anything.</span>
    </footer>}

    {overview&&(overview.synced.deleted>0||overview.synced.favourited>0)&&<dl className="kv psynced">
      <div><dt>Deleted from Photos by Cull</dt><dd>{overview.synced.deleted.toLocaleString()}</dd></div>
      <div><dt>Favourites set in Photos</dt><dd>{overview.synced.favourited.toLocaleString()}</dd></div>
      <div><dt>Last change</dt><dd>{when(overview.synced.last)}</dd></div>
    </dl>}
  </>;
}
