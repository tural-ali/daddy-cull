import {useMemo,useState,type CSSProperties} from 'react';
import {SessionVideo} from '../SessionVideo';
import type {DuplicateGroup,SidecarFacts} from './Today';
import type {Asset} from '../api';
import {requestID,sendDecisions} from './decisions';
import {Busy} from '../Busy';
import {Icon} from '../Icon';
import {usePageActions} from './pageActions';
import {useShownPath} from './libraryPath';

export type {DuplicateGroup} from './Today';
export type DuplicateMember=DuplicateGroup['members'][number];
export type DuplicateCandidate={size:number;reclaimable:number;hashed:number;members:DuplicateMember[]};
export type DuplicateReport={groups:DuplicateGroup[];unproven:DuplicateCandidate[];candidates:number;hashed:number;settled:boolean};

export function bytes(value:number){return value<1024**2?`${(value/1024).toFixed(1)} KB`:value<1024**3?`${(value/1024**2).toFixed(1)} MB`:`${(value/1024**3).toFixed(1)} GB`}
function name(path:string){return path.split('/').pop()||path}
function folder(path:string){return path.slice(0,path.lastIndexOf('/'))||'/'}


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

export type Rule='clean'|'oldest'|'newest';
const RULES:{key:Rule;label:string;hint:string}[]=[
  {key:'clean',label:'Plainest filename',hint:'Keeps the copy whose name carries no collision suffix and is not its own byte count, which is normally the original.'},
  {key:'oldest',label:'Oldest capture date',hint:'Keeps the earliest dated copy and drops later re-imports of it.'},
  {key:'newest',label:'Newest capture date',hint:'Keeps the most recently dated copy.'},
];

// What a copy's own sidecars record, in the order it would be missed: who is
// in it, then its keywords, rating and caption, then having a sidecar at all.
// Removing a copy takes its sidecars to the Bin with it.
const LORE:{of:(facts:SidecarFacts)=>number;none:string;fewer?:string}[]=[
  {of:facts=>facts.people,none:'no people tags',fewer:'fewer people tags'},
  {of:facts=>facts.keywords,none:'no keywords',fewer:'fewer keywords'},
  {of:facts=>facts.rating,none:'no rating',fewer:'lower rating'},
  {of:facts=>Number(facts.captioned),none:'no caption'},
  {of:facts=>facts.files,none:'no sidecar'},
];

/** Which of two copies' sidecars record more, first by what is hardest to
 * redo: negative when a's do. Only compared where every copy's were read, so a
 * copy not read yet is never ranked as having none. */
function lore(members:DuplicateMember[]){
  const known=members.every(member=>member.sidecars);
  return (a:DuplicateMember,b:DuplicateMember)=>{
    if(!known)return 0;
    for(const {of} of LORE){const difference=of(b.sidecars!)-of(a.sidecars!);if(difference)return difference}
    return 0;
  };
}

/** The copy to keep by a rule. A copy that records where it was taken comes
 * before one that does not, and then the one whose sidecars record more,
 * whatever the rule: keeping the other would lose the place, or the tags. */
export function pick(members:DuplicateMember[],rule:Rule):DuplicateMember{
  const ranked=[...members];
  const place=(a:DuplicateMember,b:DuplicateMember)=>Number(b.located)-Number(a.located);
  const tags=lore(members);
  const plain=(a:DuplicateMember,b:DuplicateMember)=>noise(a.path,a.size)-noise(b.path,b.size);
  if(rule==='oldest')ranked.sort((a,b)=>place(a,b)||tags(a,b)||a.capturedAt-b.capturedAt||plain(a,b));
  else if(rule==='newest')ranked.sort((a,b)=>place(a,b)||tags(a,b)||b.capturedAt-a.capturedAt||plain(a,b));
  else ranked.sort((a,b)=>place(a,b)||tags(a,b)||plain(a,b)||a.path.localeCompare(b.path));
  return ranked[0];
}

/** What a copy lacks that another copy of it has, as short flags: its place,
 * and the first thing its sidecars record less of than the richest copy's. */
export function flagsOf(member:DuplicateMember,members:DuplicateMember[]):string[]{
  const flags:string[]=[];
  if(!member.located&&members.some(other=>other.located))flags.push('no location');
  const tags=lore(members);
  const richest=[...members].sort(tags)[0];
  if(member.sidecars&&richest.sidecars&&tags(member,richest)>0){
    const missing=LORE.find(({of})=>of(richest.sidecars!)>of(member.sidecars!))!;
    flags.push(missing.of(member.sidecars)===0||!missing.fewer?missing.none:missing.fewer);
  }
  return flags;
}

function plural(count:number,one:string,many=`${one}s`){return `${count} ${count===1?one:many}`}

/** What a copy's sidecars record, as a phrase: 2 people, 3 keywords, rated 4. */
function recorded(facts:SidecarFacts){
  const parts=[facts.people&&plural(facts.people,'person','people'),facts.keywords&&plural(facts.keywords,'keyword'),facts.rating&&`rated ${facts.rating}`,facts.captioned&&'a caption'].filter(Boolean);
  return parts.join(', ');
}

function why(keeper:DuplicateMember,members:DuplicateMember[],rule:Rule){
  const others=members.filter(member=>member.id!==keeper.id);
  if(keeper.located&&others.some(member=>!member.located))
    return others.length===1?'it records where it was taken and the other copy does not':'it records where it was taken and other copies do not';
  const tags=lore(members);
  if(keeper.sidecars&&others.some(member=>tags(keeper,member)<0)){
    const said=recorded(keeper.sidecars);
    if(said)return `its sidecars record the most: ${said}`;
    return others.length===1?'it has a sidecar of its own and the other copy does not':'it has a sidecar of its own and other copies do not';
  }
  if(others.some(member=>member.path.includes('/.culled/'))&&!keeper.path.includes('/.culled/'))
    return 'the other copy sits in .culled, which was condemned once already';
  if(rule==='oldest')return 'it has the earliest capture date here';
  if(rule==='newest')return 'it has the latest capture date here';
  if(others.some(member=>{const digits=/-(\d{4,})\.[^.]+$/.exec(name(member.path));return digits!==null&&Number(digits[1])===member.size}))
    return "the other copy's name ends in its own byte count, which only an ingest script writes";
  if(others.every(member=>noise(member.path,member.size)>noise(keeper.path,keeper.size)+1))
    return 'its name carries no collision suffix, so it is most likely the original';
  return 'it sorts first, and nothing in these names distinguishes them';
}

export function dayLabel(day:string){
  const [year,month,date]=day.split('-').map(Number);
  return new Date(Date.UTC(year,month-1,date)).toLocaleDateString('en-GB',{day:'numeric',month:'long',year:'numeric',timeZone:'UTC'});
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
// or hold the same footage, so it is the same picture by definition, and one URL means one read off the
// network mount however many copies there are.
// detail goes before the size on the picture, such as the copy's pixels.
export function Tile({member,previewID,label,detail,keeper,flags,disabled,onKeep}:{member:Asset;previewID:number;label:string;detail?:string;keeper:boolean;flags:string[];disabled:boolean;onKeep:()=>void}){
  const [ratio,setRatio]=useState(0);
  const [playing,setPlaying]=useState(false);
  const [failed,setFailed]=useState(false);
  const shown=useShownPath();
  // The figure takes the photograph's own shape, so the size caption and the
  // badge sit on the picture rather than on the cell around it.
  const shape:CSSProperties|undefined=ratio?(ratio>=1?{width:'100%',height:'auto',aspectRatio:String(ratio)}:{width:'auto',height:'100%',aspectRatio:String(ratio)}):undefined;
  return <li className={`dupetile${keeper?' keeper':''}`}>
    {/* A cell as wide as the column and never taller: a landscape picture
        sets a shorter cell, so the label sits under it, not under empty space. */}
    <div className="dupecell" style={ratio>1?{aspectRatio:String(ratio)}:undefined}>
      <figure className={`dupefig${ratio||playing?' ready':''}`} style={shape}>
        {playing
          ? <SessionVideo src={`/api/media/${member.id}/original`} controls autoPlay playsInline onEnded={()=>setPlaying(false)} onError={()=>{setPlaying(false);setFailed(true)}}/>
          : failed
            ? <div className="media-missing"><span>No preview</span><small>{member.kind.toUpperCase()}</small></div>
            : <img src={`/api/media/${previewID}/preview`} alt="" loading="lazy" decoding="async"
                onLoad={event=>{const image=event.currentTarget;if(image.naturalWidth&&image.naturalHeight)setRatio(image.naturalWidth/image.naturalHeight)}}
                onError={()=>setFailed(true)}/>}
        {!playing&&<>
          <button type="button" className="dupechoose" aria-pressed={keeper} disabled={disabled}
            aria-label={keeper?`Keeping ${label}`:`Keep ${label} instead`} title={keeper?`Keeping ${shown(member.path)}`:`Keep ${shown(member.path)} instead`} onClick={onKeep}/>
          <span className={`dupemark ${keeper?'keep':'bin'}`}><Icon name={keeper?'check':'delete'}/></span>
          <span className="dupesize">{detail?`${detail} · `:''}{bytes(member.size)}</span>
          {member.kind==='video'&&<button type="button" className="duplay" aria-label={`Play ${name(member.path)}`} onClick={()=>setPlaying(true)}><Icon name="play_circle" filled/></button>}
        </>}
      </figure>
    </div>
    <p className="dupelabel" title={shown(member.path)}><span>{label}</span>{member.path.includes('/.culled/')&&!label.includes('.culled')&&<span className="dupeflag">in .culled</span>}{flags.map(flag=><span key={flag} className="dupeflag">{flag}</span>)}</p>
  </li>;
}

/** Keeps one file of each set and marks the rest for the Bin. */
export async function keepOnly(sets:{keeper:Asset;members:Asset[];favourite:boolean}[]){
  await sendDecisions(sets.flatMap(({keeper,members,favourite})=>members.map(member=>({assetId:member.id,status:member.id===keeper.id?'keep':'cull',favourite:member.id===keeper.id&&favourite,expectedRevision:member.revision,requestId:requestID()}))));
}

export function Duplicates({report}:{report:DuplicateReport}){
  const shown=useShownPath();
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
  const keeperOf=(group:DuplicateGroup)=>overrides[key(group)]??pick(group.members,rule).id;

  const active=ordered.filter(group=>!skipped.has(key(group)));
  const doomed=active.reduce((total,group)=>total+group.members.length-1,0);
  const freeing=active.reduce((total,group)=>total+group.reclaimable,0);

  async function resolve(chosen:DuplicateGroup[]){
    if(busy||chosen.length===0)return;
    setBusy(true);setError('');setMessage('');
    try{
      await keepOnly(chosen.map(group=>{
        const keeper=group.members.find(member=>member.id===keeperOf(group))||group.members[0];
        return {keeper,members:group.members,favourite:keeper.favourite};
      }));
      const gone=new Set(chosen.map(key));
      const removed=chosen.reduce((total,group)=>total+group.members.length-1,0);
      const reclaimed=chosen.reduce((total,group)=>total+group.reclaimable,0);
      setGroups(current=>current.filter(group=>!gone.has(key(group))));
      setMessage(`${removed} ${removed===1?'copy':'copies'} marked for the Bin, ${bytes(reclaimed)} in all. Nothing has moved yet: open Bin to carry it out, and it stays restorable after that.`);
    }catch(reason){setError((reason as Error).message)}finally{setBusy(false)}
  }

  // Merging everything is the page's own action, in the top bar with every
  // other page's.
  usePageActions(groups.length>0?{actions:[{label:`Merge all ${active.length.toLocaleString()}`,short:'Merge all',icon:'filter_none',keys:'Shift+M',primary:true,disabled:busy||active.length===0,onClick:()=>void resolve(active)}]}:null);

  return <section className="dupehead dupepage">
    <h1>Duplicates</h1>
    <p className="ysum"><b>{groups.length.toLocaleString()}</b> {groups.length===1?'group':'groups'} · <b>{bytes(freeing)}</b> can be freed
      <span className="dim"> · {report.hashed.toLocaleString()} of {report.candidates.toLocaleString()} possible duplicates checked</span></p>
    <p className="hint">Every group is proven on a full hash: the copies are byte-identical, or, for videos marked same footage, their pictures and sound are identical and only their metadata differs. Merging keeps the ticked copy and marks the rest for the Bin, where they stay restorable. Click another copy to keep that one instead.</p>

    {groups.length>0&&<div className="dupebulk">
      <label className="dupekeep">Keep
        <select value={rule} disabled={busy} onChange={event=>{setRule(event.target.value as Rule);setOverrides({})}}>
          {RULES.map(entry=><option key={entry.key} value={entry.key} title={entry.hint}>{entry.label.toLowerCase()}</option>)}
        </select>
      </label>
      <span className="dupespace">
        <span><b>{doomed.toLocaleString()}</b> {doomed===1?'copy':'copies'} in <b>{active.length.toLocaleString()}</b> {active.length===1?'group':'groups'}, <b>{bytes(freeing)}</b></span>
      </span>
    </div>}
    {busy&&<p className="flash" role="status"><Busy label="Saving…" state="working"/></p>}
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
      const reason=overrides[groupKey]?'you picked it':why(keeper,group.members,rule);
      return <article className={`dupegroup${skip?' skipped':''}`} key={groupKey}>
        <header className="dupegrouphead">
          <h2>{datesOf(group.members)}</h2>
          {group.proof==='footage'&&<span className="dupeproof" title="The pictures and sound are identical, byte for byte, and play the same way. Only the metadata differs, such as dates, names or where it is kept in the file.">Same footage, different metadata</span>}
          <button type="button" className="dupelink" disabled={busy||skip} title={`Keeps ${name(keeper.path)} because ${reason}, and marks the other ${group.members.length-1} for the Bin`} onClick={()=>void resolve([group])}>Merge {group.members.length} copies</button>
          <button type="button" className="dupelink quiet" disabled={busy} onClick={()=>setSkipped(current=>{const next=new Set(current);if(next.has(groupKey))next.delete(groupKey);else next.add(groupKey);return next})}>{skip?'Include':'Skip'}</button>
        </header>
        {/* One of N, never none: clicking a copy moves the tick to it, and no
            click can clear it, so a group can never have every copy marked. */}
        <ul className="dupetiles">{group.members.map(member=><Tile key={member.id} member={member} previewID={group.members[0].id}
          label={labels.get(member.id)??name(member.path)} keeper={member.id===keeperID} flags={flagsOf(member,group.members)} disabled={busy||skip}
          onKeep={()=>setOverrides(current=>({...current,[groupKey]:member.id}))}/>)}</ul>
      </article>;
    })}

    {report.unproven.length>0&&<>
      <h2 className="binsec">Possible duplicates, not proven</h2>
      <p className="hint">These files are exactly the same length as each other, which is the only way two files can be byte-identical. That is not evidence that they are: same-length files are usually different. Nothing here can be acted on until a full hash settles it, so no action is offered. At most <strong>{bytes(report.unproven.reduce((total,group)=>total+group.reclaimable,0))}</strong> is involved.</p>
      {report.unproven.map(group=><div className="xgroup" key={`size:${group.size}`}>
        <p className="xmeta">{group.members.length} files of {bytes(group.size)} · {group.hashed} of {group.members.length} hashed · up to <strong>{bytes(group.reclaimable)}</strong> if identical</p>
        <ul className="plain mono">{group.members.map(member=><li key={member.id}>{shown(member.path)} <span className="dim">· {member.day}</span></li>)}</ul>
      </div>)}
    </>}
  </section>;
}
