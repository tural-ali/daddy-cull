import {useState} from 'react';
import {Busy} from '../Busy';

export type Stats={total:number;synthetic:boolean;snapshotAt:string;candidates:number;calendarDays:number;reviewedDays:number;decisions:number;favourites:number;evidence:number;fullHashes:number;marked:number;legacyBin:number;shadowGroups:number;screenshots:number;upgradesAccepted:number;upgradeCandidates:number;bin?:number;immichSynced?:number;immichPending?:number;immichFailed?:number};

function localDate(value:string){
  if(!value)return 'never';
  return new Date(/[zZ]|[+-]\d\d:\d\d$/.test(value)?value:`${value}Z`).toLocaleString();
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
      <div><dt>Calendar days reviewed</dt><dd>{stats.reviewedDays.toLocaleString()} of {stats.calendarDays.toLocaleString()}</dd></div>
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
    <h2>Google Takeout</h2>
    <p><a href="/upgrades">Review upgrades</a> · {stats.upgradeCandidates.toLocaleString()} archive photos have confirmed higher-resolution Takeout matches. They appear beside the archive original, not as a separate collection.</p>
    <h2>Safety</h2>
    <p className="hint">The browser saves review intent only. Files marked for culling remain in the archive until you review a concrete Bin manifest. The Bin supports restore, and permanent deletion requires an exact typed confirmation.</p>
  </section>;
}
