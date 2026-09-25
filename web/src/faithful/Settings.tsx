import {useEffect,useState} from 'react';
import {Busy} from '../Busy';
import {dayStartsAt,nightStartsAt,readChoice,saveChoice,themeEvent,themeFor,type ThemeChoice} from '../theme';
import {bytes,longDate,readDeleting,type DeletingReport} from './Bin';

export type Stats={total:number;synthetic:boolean;snapshotAt:string;candidates:number;calendarDays:number;reviewedDays:number;decisions:number;favourites:number;evidence:number;fullHashes:number;marked:number;legacyBin:number;shadowGroups:number;screenshots:number;upgradesAccepted:number;upgradeCandidates:number;bin?:number;immichSynced?:number;immichPending?:number;immichFailed?:number;calendarDates?:number;reviewedDates?:number;streak?:number;reviewedToday?:boolean};

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
  useEffect(()=>{if(report&&location.hash==='#bin')document.getElementById('bin')?.scrollIntoView()},[report!==null]);
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
      if(!response.ok)throw new Error((await response.text()).trim()||'The setting could not be saved.');
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
      location.reload();
    }catch(error){setMessage((error as Error).message);setBusy(false)}
  }
  return <section className="settings">
    <h1>Settings</h1>
    {message&&<p className="flash" role="status">{busy?<Busy label={message} state="solving"/>:message}</p>}
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
      {stats.immichSynced!==undefined&&<div><dt>Favourites in Immich</dt><dd>{stats.immichSynced.toLocaleString()} synced · {(stats.immichPending??0).toLocaleString()} waiting · {(stats.immichFailed??0).toLocaleString()} failed</dd></div>}
      <div><dt>Review decisions</dt><dd>{stats.decisions.toLocaleString()}</dd></div>
      <div><dt>Shadow groups</dt><dd>{stats.shadowGroups.toLocaleString()}</dd></div>
      <div><dt>Screenshots waiting</dt><dd>{stats.screenshots.toLocaleString()}</dd></div>
      <div><dt>Accepted upgrades</dt><dd>{stats.upgradesAccepted.toLocaleString()}</dd></div>
      <div><dt>Legacy Bin files</dt><dd>{stats.legacyBin.toLocaleString()}</dd></div>
    </dl>
    <BinSettings/>
    <Appearance/>
    <h2>Google Takeout</h2>
    <p><a href="/upgrades">Review upgrades</a> · {stats.upgradeCandidates.toLocaleString()} archive photos have confirmed higher-resolution Takeout matches. They appear beside the archive original, not as a separate collection.</p>
    <h2>Safety</h2>
    <p className="hint">The browser saves review intent only. Files marked for culling stay in the archive until they are moved to the Bin, and everything in the Bin can be restored. Deleting from the Bin asks for confirmation first, and keeps the files for the grace period above before they are gone.</p>
  </section>;
}
