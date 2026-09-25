import {useMemo,useState,type CSSProperties} from 'react';
import type {DuplicateGroup} from './Today';
import {binChanged} from '../api';
import {Busy} from '../Busy';
import {Icon} from '../Icon';

export type {DuplicateGroup} from './Today';
export type DuplicateMember=DuplicateGroup['members'][number];
export type DuplicateCandidate={size:number;reclaimable:number;hashed:number;members:DuplicateMember[]};
export type DuplicateReport={groups:DuplicateGroup[];unproven:DuplicateCandidate[];candidates:number;hashed:number;settled:boolean};

function requestID(){return Array.from(crypto.getRandomValues(new Uint8Array(16)),byte=>byte.toString(16).padStart(2,'0')).join('')}
function bytes(value:number){return value<1024**2?`${(value/1024).toFixed(1)} KB`:value<1024**3?`${(value/1024**2).toFixed(1)} MB`:`${(value/1024**3).toFixed(1)} GB`}
function name(path:string){return path.split('/').pop()||path}
function folder(path:string){return path.slice(0,path.lastIndexOf('/'))||'/'}

const PER_REQUEST=20;
function chunk<T>(values:T[],size:number){const out:T[][]=[];for(let i=0;i<values.length;i+=size)out.push(values.slice(i,i+size));return out}

// How much a filename reads as machine-written rather than original. The archive
// has two known duplicate factories, Lightroom's cloud-download collisions and
// the ingest scripts' collision suffixes, and both mangle the name while leaving
// the original's alone, so the plainer name is the better keeper.
//
// The strongest signal is checkable rather than guessed: when the trailing
// number equals the file's own byte count, no camera or person produced that
// name, so it ranks below any collision suffix.
function noise(path:string,size:number){
  const file=name(path);
  const trailing=/-(\d{4,})\.[^.]+$/.exec(file);
  let score=0;
  if(path.includes('/.culled/'))score+=100;                        // condemned once already
  if(trailing)score+=Number(trailing[1])===size?9:5;               // "B612-220817108.mp4"
  if(/\(\d+\)/.test(file))score+=6;                                // "B612 (31).mp4"
  if(/\(\d{4}-\d{2}-\d{2}\)/.test(file))score+=4;                  // "B612 (2022-02-06).mp4"
  if(/\bcopy\b/i.test(file))score+=4;
  score+=file.length/100;                                          // tie-break, never dominant
  return score;
}

type Rule='clean'|'oldest'|'newest';
const RULES:{key:Rule;label:string;hint:string}[]=[
  {key:'clean',label:'Plainest filename',hint:'Keeps the copy whose name carries no collision suffix and is not its own byte count, which is normally the original.'},
  {key:'oldest',label:'Oldest capture date',hint:'Keeps the earliest dated copy and drops later re-imports of it.'},
  {key:'newest',label:'Newest capture date',hint:'Keeps the most recently dated copy.'},
];

function pick(members:DuplicateMember[],size:number,rule:Rule):DuplicateMember{
  const ranked=[...members];
  if(rule==='oldest')ranked.sort((a,b)=>a.capturedAt-b.capturedAt||noise(a.path,size)-noise(b.path,size));
  else if(rule==='newest')ranked.sort((a,b)=>b.capturedAt-a.capturedAt||noise(a.path,size)-noise(b.path,size));
  else ranked.sort((a,b)=>noise(a.path,size)-noise(b.path,size)||a.path.localeCompare(b.path));
  return ranked[0];
}

function why(keeper:DuplicateMember,members:DuplicateMember[],size:number,rule:Rule){
  const others=members.filter(member=>member.id!==keeper.id);
  if(others.some(member=>member.path.includes('/.culled/'))&&!keeper.path.includes('/.culled/'))
    return 'the other copy sits in .culled, which was condemned once already';
  if(rule==='oldest')return 'it has the earliest capture date here';
  if(rule==='newest')return 'it has the latest capture date here';
  if(others.some(member=>{const digits=/-(\d{4,})\.[^.]+$/.exec(name(member.path));return digits!==null&&Number(digits[1])===size}))
    return "the other copy's name ends in its own byte count, which only an ingest script writes";
  if(others.every(member=>noise(member.path,size)>noise(keeper.path,size)+1))
    return 'its name carries no collision suffix, so it is most likely the original';
  return 'it sorts first, and nothing in these names distinguishes them';
}

function dayLabel(day:string){
  const [year,month,date]=day.split('-').map(Number);
  return new Date(Date.UTC(year,month-1,date)).toLocaleDateString(undefined,{day:'numeric',month:'long',year:'numeric',timeZone:'UTC'});
}

// The group's heading is its dates, as Apple Photos heads a duplicate set.
function datesOf(members:DuplicateMember[]){
  const labels=[...new Set(members.map(member=>member.day))].sort().map(dayLabel);
  if(labels.length<=1)return labels[0]??'Date unknown';
  if(labels.length<=3)return `${labels.slice(0,-1).join(', ')} & ${labels[labels.length-1]}`;
  return `${labels[0]} & ${labels.length-1} more dates`;
}

const latestDay=(group:DuplicateGroup)=>group.members.reduce((latest,member)=>member.day>latest?member.day:latest,'');

// What tells the copies apart under each photo: the names when they differ,
// otherwise the part of the folder that differs, since byte-identical files
// differ in nothing else.
function labelsOf(members:DuplicateMember[]){
  if(new Set(members.map(member=>name(member.path))).size>1)return new Map(members.map(member=>[member.id,name(member.path)]));
  const folders=members.map(member=>folder(member.path).split('/'));
  let shared=0;
  while(folders.every(parts=>shared<parts.length&&parts[shared]===folders[0][shared]))shared++;
  return new Map(members.map((member,index)=>{
    const parts=folders[index];
    return [member.id,parts.slice(shared).join('/')||parts[parts.length-1]||'/'];
  }));
}

// Every tile in a group draws the same preview. The copies are byte-identical,
// so it is the same picture by definition, and one URL means one read off the
// network mount however many copies there are.
function Tile({member,previewID,size,label,keeper,disabled,onKeep}:{member:DuplicateMember;previewID:number;size:number;label:string;keeper:boolean;disabled:boolean;onKeep:()=>void}){
  const [ratio,setRatio]=useState(0);
  const [playing,setPlaying]=useState(false);
  const [failed,setFailed]=useState(false);
  // The figure takes the photograph's own shape, so the size caption and the
  // badge sit on the picture rather than on the cell around it.
  const shape:CSSProperties|undefined=ratio?(ratio>=1?{width:'100%',height:'auto',aspectRatio:String(ratio)}:{width:'auto',height:'100%',aspectRatio:String(ratio)}):undefined;
  return <li className={`dupetile${keeper?' keeper':''}`}>
    {/* A cell as wide as the column and never taller: a landscape picture
        sets a shorter cell, so the label sits under it, not under empty space. */}
    <div className="dupecell" style={ratio>1?{aspectRatio:String(ratio)}:undefined}>
      <figure className={`dupefig${ratio||playing?' ready':''}`} style={shape}>
        {playing
          ? <video src={`/api/media/${member.id}/original`} controls autoPlay playsInline onEnded={()=>setPlaying(false)} onError={()=>{setPlaying(false);setFailed(true)}}/>
          : failed
            ? <div className="media-missing"><span>No preview</span><small>{member.kind.toUpperCase()}</small></div>
            : <img src={`/api/media/${previewID}/preview`} alt="" loading="lazy" decoding="async"
                onLoad={event=>{const image=event.currentTarget;if(image.naturalWidth&&image.naturalHeight)setRatio(image.naturalWidth/image.naturalHeight)}}
                onError={()=>setFailed(true)}/>}
        {!playing&&<>
          <button type="button" className="dupechoose" aria-pressed={keeper} disabled={disabled}
            aria-label={keeper?`Keeping ${label}`:`Keep ${label} instead`} title={keeper?`Keeping ${member.path}`:`Keep ${member.path} instead`} onClick={onKeep}/>
          <span className={`dupemark ${keeper?'keep':'bin'}`}><Icon name={keeper?'check':'delete'}/></span>
          <span className="dupesize">{bytes(size)}</span>
          {member.kind==='video'&&<button type="button" className="duplay" aria-label={`Play ${name(member.path)}`} onClick={()=>setPlaying(true)}><Icon name="play_circle" filled/></button>}
        </>}
      </figure>
    </div>
    <p className="dupelabel" title={member.path}><span>{label}</span>{member.path.includes('/.culled/')&&!label.includes('.culled')&&<span className="dupeflag">in .culled</span>}</p>
  </li>;
}

export function Duplicates({report}:{report:DuplicateReport}){
  // A set of one is not a set; the server never sends one, and the page would
  // offer to merge a file with nothing if it did.
  const [groups,setGroups]=useState(()=>report.groups.filter(group=>group.members.length>1));
  const [rule,setRule]=useState<Rule>('clean');
  const [overrides,setOverrides]=useState<Record<string,number>>({});
  const [skipped,setSkipped]=useState<Set<string>>(new Set());
  const [busy,setBusy]=useState(false);
  const [message,setMessage]=useState('');
  const [error,setError]=useState('');

  const key=(group:DuplicateGroup)=>`${group.hash}:${group.size}`;
  // Newest first under date headings, as Apple Photos lists duplicates: a
  // date is what a reader recognises a picture by.
  const ordered=useMemo(()=>[...groups].sort((a,b)=>latestDay(b).localeCompare(latestDay(a))||b.reclaimable-a.reclaimable),[groups]);
  const keeperOf=(group:DuplicateGroup)=>overrides[key(group)]??pick(group.members,group.size,rule).id;

  const active=ordered.filter(group=>!skipped.has(key(group)));
  const doomed=active.reduce((total,group)=>total+group.members.length-1,0);
  const freeing=active.reduce((total,group)=>total+group.reclaimable,0);

  async function resolve(chosen:DuplicateGroup[]){
    if(busy||chosen.length===0)return;
    setBusy(true);setError('');setMessage('');
    const changes=chosen.flatMap(group=>{
      const keeperID=keeperOf(group);
      return group.members.map(member=>({assetId:member.id,status:member.id===keeperID?'keep':'cull',favourite:member.favourite,expectedRevision:member.revision,requestId:requestID()}));
    });
    let saved=0;
    try{
      for(const batch of chunk(changes,PER_REQUEST)){
        const response=await fetch('/api/decisions/batch',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(batch)});
        if(!response.ok)throw new Error(response.status===409
          ?`One of these files changed in another session. ${saved} decisions were saved; reload before continuing.`
          :`The choices could not be confirmed. ${saved} decisions were saved; reload to check before continuing.`);
        saved+=batch.length;
      }
      binChanged();
      const gone=new Set(chosen.map(key));
      const removed=chosen.reduce((total,group)=>total+group.members.length-1,0);
      const reclaimed=chosen.reduce((total,group)=>total+group.reclaimable,0);
      setGroups(current=>current.filter(group=>!gone.has(key(group))));
      setMessage(`${removed} ${removed===1?'copy':'copies'} marked for the Bin, ${bytes(reclaimed)} in all. Nothing has moved yet: open Bin to carry it out, and it stays restorable after that.`);
    }catch(reason){setError((reason as Error).message)}finally{setBusy(false)}
  }

  return <section className="dupehead dupepage">
    <h1>Duplicates</h1>
    <p className="ysum"><b>{groups.length.toLocaleString()}</b> {groups.length===1?'group':'groups'} · <b>{bytes(freeing)}</b> can be freed
      <span className="dim"> · {report.hashed.toLocaleString()} of {report.candidates.toLocaleString()} possible duplicates checked</span></p>
    <p className="hint">Every group is byte-identical on a full hash, so the copies are the same file. Merging keeps the ticked copy and marks the rest for the Bin, where they stay restorable. Click another copy to keep that one instead.</p>

    {groups.length>0&&<div className="dupebulk">
      <label className="dupekeep">Keep
        <select value={rule} disabled={busy} onChange={event=>{setRule(event.target.value as Rule);setOverrides({})}}>
          {RULES.map(entry=><option key={entry.key} value={entry.key} title={entry.hint}>{entry.label.toLowerCase()}</option>)}
        </select>
      </label>
      <span className="dupespace">
        <span><b>{doomed.toLocaleString()}</b> {doomed===1?'copy':'copies'} in <b>{active.length.toLocaleString()}</b> {active.length===1?'group':'groups'}, <b>{bytes(freeing)}</b></span>
        <button type="button" className="btn primary" disabled={busy||active.length===0} onClick={()=>void resolve(active)}>{busy?<Busy label="Saving…" state="working"/>:`Merge all ${active.length.toLocaleString()}`}</button>
      </span>
    </div>}
    {message&&<p className="flash" role="status">{message}</p>}
    {error&&<p className="note warn" role="alert">{error}</p>}

    {groups.length===0&&(report.settled
      ? <p className="note ok">{report.candidates===0
          ? 'No two files in this catalogue are even the same length, so there is nothing that could be a byte-identical duplicate.'
          : `All ${report.candidates.toLocaleString()} files that share a size with another file have been hashed, and none of them match. There are no byte-identical duplicates.`}</p>
      : <p className="note warn">This page cannot yet tell you whether duplicates exist. {report.hashed.toLocaleString()} of the {report.candidates.toLocaleString()} files that share a size with another file have a cached full hash, so {(report.candidates-report.hashed).toLocaleString()} remain unchecked. An empty result here would mean the evidence is missing, not that the archive is clean.</p>)}

    {ordered.map(group=>{
      const groupKey=key(group);
      const keeperID=keeperOf(group);
      const keeper=group.members.find(member=>member.id===keeperID)||group.members[0];
      const labels=labelsOf(group.members);
      const skip=skipped.has(groupKey);
      const reason=overrides[groupKey]?'you picked it':why(keeper,group.members,group.size,rule);
      return <article className={`dupegroup${skip?' skipped':''}`} key={groupKey}>
        <header className="dupegrouphead">
          <h2>{datesOf(group.members)}</h2>
          <button type="button" className="dupelink" disabled={busy||skip} title={`Keeps ${name(keeper.path)} because ${reason}, and marks the other ${group.members.length-1} for the Bin`} onClick={()=>void resolve([group])}>Merge {group.members.length} copies</button>
          <button type="button" className="dupelink quiet" disabled={busy} onClick={()=>setSkipped(current=>{const next=new Set(current);if(next.has(groupKey))next.delete(groupKey);else next.add(groupKey);return next})}>{skip?'Include':'Skip'}</button>
        </header>
        {/* One of N, never none: clicking a copy moves the tick to it, and no
            click can clear it, so a group can never have every copy marked. */}
        <ul className="dupetiles">{group.members.map(member=><Tile key={member.id} member={member} previewID={group.members[0].id} size={group.size}
          label={labels.get(member.id)??name(member.path)} keeper={member.id===keeperID} disabled={busy||skip}
          onKeep={()=>setOverrides(current=>({...current,[groupKey]:member.id}))}/>)}</ul>
      </article>;
    })}

    {report.unproven.length>0&&<>
      <h2 className="binsec">Possible duplicates, not proven</h2>
      <p className="hint">These files are exactly the same length as each other, which is the only way two files can be byte-identical. That is not evidence that they are: same-length files are usually different. Nothing here can be acted on until a full hash settles it, so no action is offered. At most <strong>{bytes(report.unproven.reduce((total,group)=>total+group.reclaimable,0))}</strong> is involved.</p>
      {report.unproven.map(group=><div className="xgroup" key={`size:${group.size}`}>
        <p className="xmeta">{group.members.length} files of {bytes(group.size)} · {group.hashed} of {group.members.length} hashed · up to <strong>{bytes(group.reclaimable)}</strong> if identical</p>
        <ul className="plain mono">{group.members.map(member=><li key={member.id}>{member.path} <span className="dim">· {member.day}</span></li>)}</ul>
      </div>)}
    </>}
  </section>;
}
