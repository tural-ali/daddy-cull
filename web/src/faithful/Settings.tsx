import {useEffect,useState} from 'react';
import {Busy} from '../Busy';
import {dayStartsAt,nightStartsAt,readChoice,saveChoice,themeEvent,themeFor,type ThemeChoice} from '../theme';
import {bytes,longDate,readDeleting,type DeletingReport} from './Bin';
import {clearSessionSound,setVideoSoundPreference} from '../SessionVideo';
import {reloadPage} from './router';
import {failure} from '../api';
import {readSetup,type SetupView} from './Setup';
import {hiddenGuides,showAllGuides} from './PageGuide';

export type Stats={total:number;synthetic:boolean;snapshotAt:string;candidates:number;calendarDays:number;reviewedDays:number;decisions:number;favourites:number;evidence:number;fullHashes:number;marked:number;legacyBin:number;shadowGroups:number;screenshots:number;upgradesAccepted:number;upgradeCandidates:number;videoMuted?:boolean;bin?:number;immichSynced?:number;immichPending?:number;immichFailed?:number;immichRefused?:number;calendarDates?:number;reviewedDates?:number;streak?:number;reviewedToday?:boolean;notifications?:number};

function localDate(value:string){
  if(!value)return 'never';
  return new Date(/[zZ]|[+-]\d\d:\d\d$/.test(value)?value:`${value}Z`).toLocaleString();
}

function ago(value:string){
  const at=Date.parse(value);
  if(!Number.isFinite(at))return 'never';
  const minutes=Math.round((Date.now()-at)/60000);
  if(minutes<1)return 'just now';
  if(minutes<60)return `${minutes} minute${minutes===1?'':'s'} ago`;
  const hours=Math.round(minutes/60);
  if(hours<48)return `${hours} hour${hours===1?'':'s'} ago`;
  return `${Math.round(hours/24)} days ago`;
}

// BinSettings sets how long files deleted from the Bin are kept, and shows that
// automatic deletion is actually running: what is waiting, what goes next, and
// what the last check did.
function BinSettings(){
  const [report,setReport]=useState<DeletingReport|null>(null);
  const [days,setDays]=useState('');
  const [busy,setBusy]=useState(false);
  const [message,setMessage]=useState('');
  const [error,setError]=useState('');
  useEffect(()=>{
    readDeleting().then(next=>{setReport(next);setDays(String(next.graceDays))}).catch(reason=>setError((reason as Error).message));
  },[]);
  // The page is drawn after its data arrives, so a link to /settings#bin lands
  // at the top unless the section is scrolled to once it exists.
  const reported=report!==null;
  useEffect(()=>{if(reported&&location.hash==='#bin')document.getElementById('bin')?.scrollIntoView()},[reported]);
  const value=Number(days);
  const valid=/^\d{1,3}$/.test(days)&&value<=365;
  const changed=report!==null&&valid&&value!==report.graceDays;
  const items=report?.items??[];
  // Shortening the period applies to what is already waiting, so the page says
  // how many files that would send at the next check before it is saved.
  const soon=valid?items.filter(item=>Date.parse(item.deletedAt)+value*86400000<=Date.now()).length:0;
  const next=items[0];
  async function save(){
    setBusy(true);setMessage('');setError('');
    try{
      const response=await fetch('/api/settings/bin',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({graceDays:value})});
      if(!response.ok)throw new Error(await failure(response,'The setting could not be saved.'));
      const saved:DeletingReport=await response.json();
      setReport(saved);setDays(String(saved.graceDays));
      setMessage(saved.graceDays===0?'Saved. Deleting from the Bin is now immediate.':`Saved. Deleted files are now kept for ${saved.graceDays} day${saved.graceDays===1?'':'s'}.`);
    }catch(reason){setError((reason as Error).message)}finally{setBusy(false)}
  }
  return <>
    <h2 id="bin">Bin</h2>
    <p className="hint">Files you delete from the Bin stay on disk this long, restorable from the <a href="/log">Log</a>, and are then deleted automatically. Zero deletes them at once. A change also applies to files already waiting.</p>
    {error&&<p className="note warn" role="alert">{error}</p>}
    {message&&<p className="flash" role="status">{message}</p>}
    {report?.graceError&&<p className="note warn" role="alert">Automatic deletion is paused: {report.graceError} Save a number of days to resume it.</p>}
    <form className="graceform" onSubmit={event=>{event.preventDefault();if(changed||report?.graceError)void save()}}>
      <label htmlFor="grace">Keep deleted files for</label>
      <input id="grace" type="number" inputMode="numeric" min={0} max={365} step={1} value={days} disabled={report===null||busy} aria-invalid={!valid} onChange={event=>setDays(event.target.value)}/>
      <span>day{value===1?'':'s'}</span>
      <button className="btn primary" disabled={busy||!valid||!(changed||report?.graceError)}>{busy?<Busy label="Saving…" state="working"/>:'Save'}</button>
    </form>
    {!valid&&days!==''&&<p className="note warn">Choose a whole number of days from 0 to 365.</p>}
    {changed&&soon>0&&<p className="note warn">{soon.toLocaleString()} file{soon===1?' has':'s have'} already waited longer than {value} day{value===1?'':'s'} and will be deleted at the next check, within {report?.checkIntervalMinutes??15} minutes.</p>}
    {report===null&&!error&&<p className="hint"><Busy label="Reading the deletion schedule…"/></p>}
    {report&&<dl className="kv">
      <div><dt>Waiting to be deleted</dt><dd>{items.length===0?'nothing':<><a href="/log">{items.length.toLocaleString()} file{items.length===1?'':'s'}</a> · {bytes(items.reduce((sum,item)=>sum+item.size,0))}</>}</dd></div>
      <div><dt>Next to go</dt><dd>{next?<>{next.name} on {longDate(next.dueAt)}</>:'nothing is scheduled'}</dd></div>
      <div><dt>Last automatic check</dt><dd>{report.lastRun?<>{ago(report.lastRun)}, {report.lastDeleted>0?`deleted ${report.lastDeleted.toLocaleString()} file${report.lastDeleted===1?'':'s'}`:'nothing was due'}</>:'not yet run'}</dd></div>
      {report.lastError&&<div><dt>Problem</dt><dd className="warntext">{report.lastError}</dd></div>}
      <div><dt>Checks</dt><dd>every {report.checkIntervalMinutes} minutes, by the private writer</dd></div>
    </dl>}
  </>;
}

const themeChoices:{value:ThemeChoice;label:string}[]=[
  {value:'auto',label:'Automatic'},
  {value:'day',label:'Day'},
  {value:'night',label:'Night'},
];
const hour=(value:number)=>`${String(value).padStart(2,'0')}:00`;

// Day and night by the reader's clock, or pinned. Kept in this browser only.
function Appearance(){
  const [choice,setChoice]=useState<ThemeChoice>(readChoice);
  const [now,setNow]=useState(()=>themeFor(readChoice()));
  useEffect(()=>{
    const sync=()=>{setChoice(readChoice());setNow(themeFor(readChoice()))};
    window.addEventListener(themeEvent,sync);
    return()=>window.removeEventListener(themeEvent,sync);
  },[]);
  return <>
    <h2 id="appearance">Appearance</h2>
    <div className="segmented" role="radiogroup" aria-label="Theme">
      {themeChoices.map(option=><label key={option.value} className={choice===option.value?'on':undefined}>
        <input type="radio" name="theme" value={option.value} checked={choice===option.value} onChange={()=>saveChoice(option.value)}/>{option.label}
      </label>)}
    </div>
    <p className="hint">{choice==='auto'
      ?<>Day from {hour(dayStartsAt)} to {hour(nightStartsAt)} by this device's clock, night otherwise. It is {now} now.</>
      :<>Always {choice}, on this browser only.</>}</p>
  </>;
}

// VideoSettings chooses whether clips start muted. Saving applies to this tab
// at once, dropping any sound choice made in it, and to every other browser.
function VideoSettings({initial}:{initial:boolean}){
  const [muted,setMuted]=useState(initial);
  const [busy,setBusy]=useState(false);
  const [error,setError]=useState('');
  async function choose(next:boolean){
    if(next===muted||busy)return;
    setBusy(true);setError('');
    try{
      const response=await fetch('/api/settings/video',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({muted:next})});
      if(!response.ok)throw new Error(await failure(response,'The setting could not be saved.'));
      const saved:{muted:boolean}=await response.json();
      setMuted(saved.muted);setVideoSoundPreference(saved.muted);clearSessionSound();
    }catch(reason){setError((reason as Error).message)}finally{setBusy(false)}
  }
  return <>
    <h2 id="video">Video</h2>
    <div className="segmented" role="radiogroup" aria-label="Video sound">
      <label className={muted?'on':undefined}><input type="radio" name="video-sound" value="muted" checked={muted} disabled={busy} onChange={()=>void choose(true)}/>Start muted</label>
      <label className={muted?undefined:'on'}><input type="radio" name="video-sound" value="sound" checked={!muted} disabled={busy} onChange={()=>void choose(false)}/>Start with sound</label>
    </div>
    {error&&<p className="note warn" role="alert">{error}</p>}
    <p className="hint">{muted?'Clips open silent. Turning the sound on in the viewer keeps it on for the rest of that tab.':'Clips open with sound. Muting one in the viewer keeps the rest of that tab quiet.'}</p>
  </>;
}

type IntakeStatus={folder:string;lastRun:string;filed:number;filedTotal:number;duplicates:number;waiting:number;unsupported:number;problem?:string};
type IntakeView={configured:boolean;status:IntakeStatus|null};

async function readIntake():Promise<IntakeView>{
  const response=await fetch('/api/intake',{cache:'no-store'});
  if(!response.ok)throw new Error(await failure(response,'The Import folder could not be read.'));
  return response.json() as Promise<IntakeView>;
}

// FolderSettings shows where the library and the Import folder are, what the
// writer last found in the Import folder, and how each download last went.
function FolderSettings(){
  const [setup,setSetup]=useState<SetupView|null>(null);
  const [intake,setIntake]=useState<IntakeView|null>(null);
  const [busy,setBusy]=useState(false);
  const [message,setMessage]=useState('');
  const [error,setError]=useState('');
  useEffect(()=>{
    readSetup().then(setSetup).catch(reason=>setError((reason as Error).message));
    readIntake().then(setIntake).catch(reason=>setError((reason as Error).message));
  },[]);
  async function look(){
    setBusy(true);setMessage('');setError('');
    try{
      const response=await fetch('/api/intake/run',{method:'POST',headers:{'Content-Type':'application/json'},body:'{}'});
      if(!response.ok)throw new Error(await failure(response,'The Import folder could not be looked at.'));
      const status:IntakeStatus=await response.json();
      setIntake({configured:true,status});
      setMessage(status.filed>0?`Filed ${status.filed.toLocaleString()} photo${status.filed===1?'':'s'}.`:'Nothing new to file.');
    }catch(reason){setError((reason as Error).message)}finally{setBusy(false)}
  }
  const status=intake?.status??null;
  const config=setup?.config;
  return <>
    <h2 id="folders">Folders</h2>
    {!setup&&!error&&<p className="hint"><Busy label="Reading the folders…"/></p>}
    {config&&<dl className="kv">
      <div><dt>Library</dt><dd>{config.library||'Not chosen'}</dd></div>
      <div><dt>Import folder</dt><dd>{config.import||'None'}</dd></div>
      <div><dt>iCloud Photos</dt><dd>{config.icloud.on?<>{config.icloud.appleId}{setup.icloud.lastRun?<>, last run {ago(setup.icloud.lastRun)}</>:', not run yet'}</>:'Off'}</dd></div>
      <div><dt>Google Takeout folder</dt><dd>{config.takeoutInbox||'None'}</dd></div>
    </dl>}
    {setup?.config.icloud.on&&setup.icloud.lastRun&&!setup.icloud.ok&&<p className="note warn">iCloud: {setup.icloud.message||'the last download did not finish.'} <a href="/setup?step=icloud">Sign in again</a></p>}
    {status&&<>
      <p className="hint">The Import folder is looked at every minute. Each photo is filed under the day it was taken; one the library already has is moved into <b>Already in the library</b> inside the Import folder, never deleted.</p>
      <dl className="kv">
        <div><dt>Last looked</dt><dd>{status.lastRun?ago(status.lastRun):'Not yet'}</dd></div>
        <div><dt>Filed</dt><dd>{status.filed.toLocaleString()} last time · {status.filedTotal.toLocaleString()} since Cull started</dd></div>
        <div><dt>Already in the library</dt><dd>{status.duplicates.toLocaleString()}</dd></div>
        <div><dt>Still arriving</dt><dd>{status.waiting.toLocaleString()}</dd></div>
        <div><dt>Not photos or videos</dt><dd>{status.unsupported.toLocaleString()}, left where they are</dd></div>
      </dl>
      {status.problem&&<p className="note warn">{status.problem}</p>}
    </>}
    {message&&<p className="flash" role="status">{message}</p>}
    {error&&<p className="note warn" role="alert">{error}</p>}
    <div className="setupacts inline">
      {intake?.configured&&<button type="button" className="btn primary" disabled={busy} onClick={()=>void look()}>{busy?<Busy label="Looking…" state="working"/>:'Look at the Import folder now'}</button>}
      {setup&&<a className="btn" href="/setup?step=folders">{setup.configurable?'Change in setup':'Open setup'}</a>}
    </div>
  </>;
}

// Guides brings back the guide at the top of each page, once hidden.
function Guides(){
  const [hidden,setHidden]=useState(hiddenGuides);
  const [message,setMessage]=useState('');
  return <>
    <h2 id="guides">Page guides</h2>
    <p className="hint">Each page starts with a short guide to how it works, until you hide it. The question mark at the top of a page shows its guide again.</p>
    {message&&<p className="flash" role="status">{message}</p>}
    <button type="button" className="btn" disabled={hidden===0} onClick={()=>{const count=showAllGuides();setHidden(0);setMessage(`${count} guide${count===1?'':'s'} will show again.`)}}>{hidden===0?'Every guide is showing':`Show all guides again (${hidden} hidden)`}</button>
  </>;
}

export function Settings({stats}:{stats:Stats}){
  const [busy,setBusy]=useState(false);
  const [message,setMessage]=useState('');
  async function reindex(){
    setBusy(true);
    setMessage('Refreshing the metadata index…');
    try{
      const response=await fetch('/api/reindex',{method:'POST',headers:{'Content-Type':'application/json'},body:'{}'});
      if(!response.ok)throw new Error('The index refresh did not complete.');
      setMessage('Index refreshed. Reloading…');
      reloadPage();
    }catch(error){setMessage((error as Error).message);setBusy(false)}
  }
  return <section className="settings">
    <h1>Settings</h1>
    {message&&<p className="flash" role="status">{busy?<Busy label={message} state="solving"/>:message}</p>}
    <FolderSettings/>
    <h2>Index</h2>
    <p className="hint">The catalogue refresh reads metadata already imported into this service. It does not alter an original file.</p>
    <dl className="kv">
      <div><dt>Media files</dt><dd>{stats.total.toLocaleString()}</dd></div>
      <div><dt>Day folders</dt><dd>{stats.calendarDays.toLocaleString()}</dd></div>
      <div><dt>Day folders reviewed</dt><dd>{stats.reviewedDays.toLocaleString()} of {stats.calendarDays.toLocaleString()}</dd></div>
      {stats.calendarDates!==undefined&&<div><dt>Calendar dates reviewed</dt><dd>{(stats.reviewedDates??0).toLocaleString()} of {stats.calendarDates.toLocaleString()}</dd></div>}
      <div><dt>Last refreshed</dt><dd>{localDate(stats.snapshotAt)}</dd></div>
    </dl>
    <button className="btn primary" disabled={busy} onClick={()=>void reindex()}>Refresh the catalogue index</button>
    <h2>Derived data</h2>
    <dl className="kv">
      <div><dt>Files with imported evidence</dt><dd>{stats.evidence.toLocaleString()}</dd></div>
      <div><dt>Files fully hashed for duplicates</dt><dd>{stats.fullHashes.toLocaleString()}</dd></div>
      <div><dt>Favourites marked</dt><dd>{stats.favourites.toLocaleString()}</dd></div>
      {stats.immichSynced!==undefined&&<div><dt>Favourites in Immich</dt><dd>{stats.immichSynced.toLocaleString()} synced · {(stats.immichPending??0).toLocaleString()} waiting · {(stats.immichFailed??0).toLocaleString()} failed{(stats.immichRefused??0)>0&&<> · {stats.immichRefused!.toLocaleString()} belong to another Immich user</>}</dd></div>}
      <div><dt>Review decisions</dt><dd>{stats.decisions.toLocaleString()}</dd></div>
      <div><dt>Shadow groups</dt><dd>{stats.shadowGroups.toLocaleString()}</dd></div>
      <div><dt>Screenshots waiting</dt><dd>{stats.screenshots.toLocaleString()}</dd></div>
      <div><dt>Accepted upgrades</dt><dd>{stats.upgradesAccepted.toLocaleString()}</dd></div>
      <div><dt>Legacy Bin files</dt><dd>{stats.legacyBin.toLocaleString()}</dd></div>
    </dl>
    <BinSettings/>
    <VideoSettings initial={stats.videoMuted!==false}/>
    <Appearance/>
    <Guides/>
    <h2>Google Takeout</h2>
    <p><a href="/upgrades">Review upgrades</a> · {stats.upgradeCandidates.toLocaleString()} archive photos have confirmed higher-resolution Takeout matches. They appear beside the archive original, not as a separate collection.</p>
    <h2>Safety</h2>
    <p className="hint">The browser saves review intent only. Files marked for culling stay in the archive until they are moved to the Bin, and everything in the Bin can be restored. Deleting from the Bin asks for confirmation first, and keeps the files for the grace period above before they are gone.</p>
  </section>;
}
