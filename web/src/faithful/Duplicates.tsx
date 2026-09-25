import {useMemo,useState} from 'react';
import type {DuplicateGroup} from './Today';
import {binChanged} from '../api';

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

// Byte-identical files differ only in where they sit and what they are called,
// so those are the only columns worth showing. Anything the copies share is said
// once in the group header instead of repeated on every row.
function differing(members:DuplicateMember[]){
  return {
    folder:new Set(members.map(member=>folder(member.path))).size>1,
    day:new Set(members.map(member=>member.day)).size>1,
  };
}

// The copies are byte-identical, so they are the same picture by definition and
// one frame answers for the whole group. It is also the only affordable choice:
// the archive is a network mount, and a video element per copy turned two tiles
// into five hundred range requests.
function Frame({member}:{member:DuplicateMember}){
  const [playing,setPlaying]=useState(false);
  const [failed,setFailed]=useState(false);
  if(playing)return <video className="dupemedia" src={`/api/media/${member.id}/original`} controls autoPlay playsInline
    onEnded={()=>setPlaying(false)} onError={()=>{setPlaying(false);setFailed(true)}}/>;
  return <>
    {failed
      ? <div className="media-missing"><span>No frame</span><small>{member.kind.toUpperCase()}</small></div>
      : <img className="dupemedia" src={`/api/media/${member.id}/preview`} alt="" loading="lazy" decoding="async" onError={()=>setFailed(true)}/>}
    {member.kind==='video'&&<button type="button" className="socialplay" aria-label={`Play ${name(member.path)}`} onClick={()=>setPlaying(true)}>▶</button>}
  </>;
}

export function Duplicates({report}:{report:DuplicateReport}){
  const [groups,setGroups]=useState(report.groups);
  const [rule,setRule]=useState<Rule>('clean');
  const [overrides,setOverrides]=useState<Record<string,number>>({});
  const [skipped,setSkipped]=useState<Set<string>>(new Set());
  const [busy,setBusy]=useState(false);
  const [message,setMessage]=useState('');
  const [error,setError]=useState('');

  const key=(group:DuplicateGroup)=>`${group.hash}:${group.size}`;
  // Biggest win first, which is the order every established duplicate finder
  // uses because it is the order that makes a long list worth working through.
  const ordered=useMemo(()=>[...groups].sort((a,b)=>b.reclaimable-a.reclaimable),[groups]);
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

  return <section className="dupehead">
    <h1>Exact duplicates</h1>
    <p className="ysum"><b>{groups.length.toLocaleString()}</b> verified groups · <b>{bytes(freeing)}</b> reclaimable
      <span className="dim"> · {report.hashed.toLocaleString()} of {report.candidates.toLocaleString()} possible duplicates hashed</span></p>
    <p className="hint">Two files can only be byte-identical if they are the same length, so the files that share a size with another file are the whole population that could hold a duplicate, and coverage is measured against that rather than against the archive. Every group below is byte-identical on a full hash, which means the copies are interchangeable and the only real question is which path you want to keep.</p>

    {groups.length>0&&<div className="dupebulk">
      <span className="dim">Keep by default:</span>
      {RULES.map(entry=><button type="button" key={entry.key} className={`btn small${rule===entry.key?' on':''}`} title={entry.hint}
        onClick={()=>{setRule(entry.key);setOverrides({})}}>{entry.label}</button>)}
      <span className="dupespace">
        <span>Marking <b>{doomed.toLocaleString()}</b> {doomed===1?'copy':'copies'} across <b>{active.length.toLocaleString()}</b> {active.length===1?'group':'groups'} frees <b>{bytes(freeing)}</b></span>
        <button type="button" className="btn primary" disabled={busy||active.length===0} onClick={()=>void resolve(active)}>{busy?'Saving…':`Mark ${doomed.toLocaleString()} for the Bin`}</button>
      </span>
    </div>}
    {message&&<p className="flash" role="status">{message}</p>}
    {error&&<p className="note warn" role="alert">{error}</p>}

    {groups.length===0&&(report.settled
      ? <p className="note ok">{report.candidates===0
          ? 'No two files in this catalogue are even the same length, so there is nothing that could be a byte-identical duplicate.'
          : `All ${report.candidates.toLocaleString()} files that share a size with another file have been hashed, and none of them match. There are no byte-identical duplicates.`}</p>
      : <p className="note warn">This page cannot yet tell you whether duplicates exist. {report.hashed.toLocaleString()} of the {report.candidates.toLocaleString()} files that share a size with another file have a cached full hash, so {(report.candidates-report.hashed).toLocaleString()} remain unchecked. An empty result here would mean the evidence is missing, not that the archive is clean.</p>)}

    {ordered.map((group,index)=>{
      const groupKey=key(group);
      const keeperID=keeperOf(group);
      const keeper=group.members.find(member=>member.id===keeperID)||group.members[0];
      const shows=differing(group.members);
      const skip=skipped.has(groupKey);
      return <div className={`xgroup dupegroup${skip?' skipped':''}`} key={groupKey}>
        <p className="xmeta"><span className="gnum">{index+1}</span>
          {group.members.length} identical copies · {bytes(group.size)} each · <strong>{bytes(group.reclaimable)}</strong> reclaimable
          {!shows.folder&&<span className="dim"> · all in {folder(keeper.path)}</span>}
          {!shows.day&&<span className="dim"> · {keeper.day}</span>}
          <span className="hash">{group.hash.slice(0,12)}</span>
        </p>
        <div className="dupebody">
          <div className="dupeshot"><Frame member={keeper}/></div>
          <div className="dupepick">
            <p className="dupewhy">{overrides[groupKey]
              ? <>Keeping <strong>{name(keeper.path)}</strong> because you picked it.</>
              : <>Keeping <strong>{name(keeper.path)}</strong> because {why(keeper,group.members,group.size,rule)}.</>}</p>
            {/* One of N, and a radio cannot be cleared, only moved. That is what
                makes the keeper protected: there is no state in which a group
                has every one of its copies marked. */}
            <ul className="dupelist">{group.members.map(member=>{
              const isKeeper=member.id===keeperID;
              return <li key={member.id} className={isKeeper?'keeper':'doomed'}>
                <label>
                  <input type="radio" name={`keeper-${groupKey}`} checked={isKeeper} disabled={busy||skip}
                    onChange={()=>setOverrides(current=>({...current,[groupKey]:member.id}))}/>
                  <span className="dupename" title={member.path}>{name(member.path)}</span>
                  {shows.folder&&<span className="dupepath differs" title={member.path}>{folder(member.path)}</span>}
                  {shows.day&&<span className="dupeday differs">{member.day}</span>}
                  {member.path.includes('/.culled/')&&<span className="dupeflag">in .culled</span>}
                  <span className={`dupestate ${isKeeper?'keep':'cull'}`}>{isKeeper?'Keep':'Bin'}</span>
                </label>
              </li>;
            })}</ul>
            <p className="xact">
              <button className="btn small danger" disabled={busy||skip} onClick={()=>void resolve([group])}>Mark the other {group.members.length-1} for the Bin</button>
              <button className="btn small" disabled={busy} onClick={()=>setSkipped(current=>{const next=new Set(current);next.has(groupKey)?next.delete(groupKey):next.add(groupKey);return next})}>{skip?'Include this group':'Leave this group alone'}</button>
            </p>
          </div>
        </div>
      </div>;
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
