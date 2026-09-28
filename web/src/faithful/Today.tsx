import {useEffect,useEffectEvent,useMemo,useRef,useState,type ReactNode} from 'react';
import {binChanged,type Asset,type Status} from '../api';
import {Media} from '../Media';
import {useDecisionQueue} from '../useDecisionQueue';
import {calendarLabel} from './Year';
import {Viewer} from './Viewer';
import {usePhotoURL} from './photoURL';
import {Busy} from '../Busy';
import {Icon,type IconName} from '../Icon';
import {Celebration,type Tally} from './Celebration';
import {usePageActions} from './pageActions';
import {shortLabel,usePageDate} from './DatePicker';
import {dayName} from './goto';
import {requestID,sendDecisions} from './decisions';
import {historyKey,undoKeys,useHistory,type HistoryEntry,type Snapshot} from './history';
import {pairLabel,rawsBehind,setPaired} from './pairs';
import {usePageFilters,type SortOption} from './SearchFilters';
import {Snacks} from './Snacks';
import {Justified,shapeOf,shapeProps,type Box} from './justified';

// fresh counts files that reached the archive after the day was reviewed and
// still wait; each carries new.
export type TodayYear={day:string;year:number;files:number;bytes:number;status:'pending'|'done';assets:Asset[];fresh?:number};
export type TodayData={md:string;label:string;previous:string;next:string;years:TodayYear[];memories:number;bytes:number};
export type DuplicateMember=Asset&{day:string};
export type DuplicateGroup={hash:string;size:number;reclaimable:number;members:DuplicateMember[]};

/** Whether a key went to a field being typed in, which the page's single
 * letter shortcuts must leave alone. */
function typing(target:EventTarget|null){
  return target instanceof HTMLInputElement||target instanceof HTMLTextAreaElement||target instanceof HTMLSelectElement||(target instanceof HTMLElement&&target.isContentEditable);
}
function bytes(value:number){
  if(value<1024)return `${value} B`;
  if(value<1024**2)return `${(value/1024).toFixed(1)} KB`;
  if(value<1024**3)return `${(value/1024**2).toFixed(1)} MB`;
  return `${(value/1024**3).toFixed(1)} GB`;
}
/** A video's running time the way a tile shows it: "0:41", or "1:02:05"
 * past the hour. */
export function runningTime(seconds:number){
  const total=Math.max(1,Math.round(seconds));
  const hours=Math.floor(total/3600),minutes=Math.floor(total%3600/60),rest=String(total%60).padStart(2,'0');
  return hours?`${hours}:${String(minutes).padStart(2,'0')}:${rest}`:`${minutes}:${rest}`;
}
function captureTime(timestamp:number){return timestamp?new Date(timestamp*1000).toLocaleTimeString([],{hour:'2-digit',minute:'2-digit'}):null}

function snapshot(asset:Asset):Snapshot{return {id:asset.id,status:asset.status,favourite:asset.favourite}}
function fileName(asset:Asset){return asset.path.split('/').pop()??''}
/** What a single decision did, for the undo message. */
function describe(asset:Asset,status:Status,favourite:boolean){
  const name=fileName(asset);
  if(status==='cull')return `removed ${name}`;
  if(favourite!==asset.favourite)return favourite?`favourited ${name}`:`unfavourited ${name}`;
  if(status==='keep')return `kept ${name}`;
  if(asset.status==='cull')return `brought back ${name}`;
  return `cleared ${name}`;
}

type DateReviewed={days:string[];kept:{id:number;revision:number}[];tally:{total:number;removed:number;bytes:number;kept:number;favourites:number}};
/** Finishes a calendar date on the server in one step: every file still
 * undecided is kept, every year not yet reviewed is marked, and what the date
 * came to is counted from the catalogue, the Bin's emptied files included. */
async function reviewDate(md:string):Promise<DateReviewed>{
  const response=await fetch(`/api/dates/${md}/reviewed`,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({requestId:requestID()})});
  if(!response.ok)throw new Error(response.status===409?'A file on this date changed in another session. Reload before marking it reviewed.':'The review could not be saved.');
  return response.json();
}

async function setProgress(day:string,status:'pending'|'done'){
  const response=await fetch('/api/day-progress',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({day,status,requestId:requestID()})});
  if(!response.ok)throw new Error('The review marker could not be saved.');
}

/** The day's filters, which live in the search bar. One from a group narrows
 * the day to it; two from the same group widen it to either; groups combine. */
type Filter='favourites'|'videos'|'photos'|'undecided'|'kept'|'removed';
const filterChips:{id:Filter;label:string;icon:IconName;group:string;words:string[]}[]=[
  {id:'favourites',label:'Favourites',icon:'favorite',group:'Favourites',words:['favourites','favorites','hearts']},
  {id:'videos',label:'Videos',icon:'videocam',group:'Type',words:['videos','clips','movies']},
  {id:'photos',label:'Photos',icon:'image',group:'Type',words:['photos','pictures','images','raw']},
  {id:'undecided',label:'Undecided',icon:'schedule',group:'Decision',words:['undecided','unreviewed','to do','todo','not decided']},
  {id:'kept',label:'Kept',icon:'check_circle',group:'Decision',words:['kept','keep']},
  {id:'removed',label:'Removed',icon:'delete',group:'Decision',words:['removed','remove','deleted','bin']},
];
const filterKey='cull.day-filters';
const filterStatus:Record<'undecided'|'kept'|'removed',Status>={undecided:'unreviewed',kept:'keep',removed:'cull'};
/** The chips chosen on the last day, so a filter follows the reviewer from
 * day to day in this tab. */
function savedFilters():Filter[]{
  try{
    const value:unknown=JSON.parse(sessionStorage.getItem(filterKey)??'[]');
    return Array.isArray(value)?value.filter((item):item is Filter=>filterChips.some(chip=>chip.id===item)):[];
  }catch{return []}
}
function matches(asset:Asset,filters:ReadonlySet<Filter>):boolean{
  if(filters.has('favourites')&&!asset.favourite)return false;
  const videos=filters.has('videos'),photos=filters.has('photos');
  if((videos||photos)&&!(videos&&asset.kind==='video'||photos&&asset.kind!=='video'))return false;
  const statuses=(['undecided','kept','removed'] as const).filter(id=>filters.has(id)).map(id=>filterStatus[id]);
  return statuses.length===0||statuses.includes(asset.status);
}

/** How long the keyboard tip stays, and whether this page load has had it. */
const tipTime=15000;
/** How long a notice in the corner stays. */
const snackTime=6000;
let tipShown=false;
const tipKey='cull.tip';

/** Whether the tip is due: on the first day opened in this tab, and again
 * after a refresh, but not while the reviewer moves from day to day. Every
 * day link is a page load, so the load's own kind tells the two apart. */
function tipDue():boolean{
  try{
    const load=performance.getEntriesByType('navigation')[0] as PerformanceNavigationTiming|undefined;
    if(load?.type==='reload')return true;
    return sessionStorage.getItem(tipKey)===null;
  }catch{return true}
}

/** One year's photos in rows at their own shapes. */
function YearGrid({assets,render}:{assets:Asset[];render:(asset:Asset,box:Box)=>ReactNode}){
  const items=useMemo(()=>assets.map(asset=>({key:asset.id,ratio:shapeOf(asset)})),[assets]);
  return <Justified className="gal" items={items} render={(index,box)=>render(assets[index],box)}/>;
}

/** The two orders a date's years can be shown in, the oldest first as the
 * server lists them. The choice is kept on this device. */
const orders=[
  {id:'oldest',label:'Oldest year first',icon:'arrow_upward',words:['oldest','earliest','ascending','sort','order']},
  {id:'newest',label:'Newest year first',icon:'arrow_downward',words:['newest','latest','recent','descending','sort','order']},
] satisfies SortOption[];
const orderKey='cull.year-order';
function savedOrder(){try{return localStorage.getItem(orderKey)==='newest'?'newest':'oldest'}catch{return 'oldest'}}

export function Today({initial}:{initial:TodayData}){
  const [years,setYears]=useState(initial.years);
  const [order,setOrder]=useState(savedOrder);
  function saveOrder(id:string){
    const next=id==='newest'?'newest':'oldest';
    setOrder(next);
    try{if(next==='oldest')localStorage.removeItem(orderKey);else localStorage.setItem(orderKey,next)}catch{/* kept for this visit only */}
  }
  const [selected,setSelected]=useState<number|null>(null);
  // The keyboard tip comes up as a snackbar in the corner and goes on its
  // own; moving between days does not bring it back, a refresh does.
  const [tip,setTip]=useState(false);
  // The card over the day once it is marked reviewed.
  const [cheer,setCheer]=useState<Tally|null>(null);
  useEffect(()=>{
    if(tipShown)return;
    tipShown=true;
    if(!tipDue())return;
    try{sessionStorage.setItem(tipKey,'shown')}catch{/* storage blocked: the tip shows on every day */}
    setTip(true);
  },[]);
  useEffect(()=>{if(!tip)return;const timer=setTimeout(()=>setTip(false),tipTime);return()=>clearTimeout(timer)},[tip]);
  const [saving,setSaving]=useState(false);
  // A notice in the corner, gone on its own unless a save is still running.
  const [message,setMessage]=useState('');
  useEffect(()=>{if(!message||saving)return;const timer=setTimeout(()=>setMessage(''),snackTime);return()=>clearTimeout(timer)},[message,saving]);
  const [duplicateGroups,setDuplicateGroups]=useState<DuplicateGroup[]>([]);
  const [keepers,setKeepers]=useState<Record<string,number>>({});
  const history=useHistory();
  const photo=usePhotoURL(id=>initial.years.some(year=>year.assets.some(asset=>String(asset.id)===id)));
  const viewer=photo.open===null?null:Number(photo.open);
  // The years in the order shown. The grid, the arrow keys and the viewer all
  // walk this, so reading the oldest year first reviews it first too.
  const shownYears=useMemo(()=>[...years].sort((a,b)=>order==='oldest'?a.day.localeCompare(b.day):b.day.localeCompare(a.day)),[years,order]);
  const assets=useMemo(()=>shownYears.flatMap(year=>year.assets),[shownYears]);
  // A RAW+JPEG pair is one tile, the JPEG; its RAW rides along with every
  // choice and shows in the viewer on request.
  const behind=useMemo(()=>rawsBehind(assets),[assets]);
  const hidden=useMemo(()=>new Set([...behind.values()].map(raw=>raw.id)),[behind]);
  const tiles=useMemo(()=>hidden.size===0?assets:assets.filter(asset=>!hidden.has(asset.id)),[assets,hidden]);
  const [filters,setFilters]=useState<ReadonlySet<Filter>>(()=>new Set(savedFilters()));
  function saveFilters(next:ReadonlySet<Filter>){
    setFilters(next);
    try{sessionStorage.setItem(filterKey,JSON.stringify([...next]))}catch{/* storage blocked: the filters still work on this day */}
  }
  function toggleFilter(id:Filter){
    const next=new Set(filters);
    if(next.has(id))next.delete(id);else next.add(id);
    saveFilters(next);
  }
  // What the chips leave: the grid, the arrow keys and the viewer all walk
  // this list, so a filtered day reviews as one.
  const shown=useMemo(()=>filters.size===0?tiles:tiles.filter(asset=>matches(asset,filters)),[tiles,filters]);
  const shownIDs=useMemo(()=>new Set(shown.map(asset=>asset.id)),[shown]);
  const doneYears=years.filter(year=>year.status==='done').length;
  const dateDone=years.length>0&&doneYears===years.length;

  function patchAsset(id:number,change:Partial<Asset>){
    setYears(current=>current.map(year=>({...year,assets:year.assets.map(asset=>asset.id===id?{...asset,...change}:asset)})));
    setDuplicateGroups(current=>current.map(group=>({...group,members:group.members.map(asset=>asset.id===id?{...asset,...change}:asset)})));
  }
  async function resolveGroup(group:DuplicateGroup){
    if(saving)return;
    const keeperID=keepers[group.hash]??group.members[0].id;
    const changes=group.members.map(asset=>({assetId:asset.id,status:asset.id===keeperID?'keep':'cull',favourite:asset.id===keeperID&&asset.favourite,expectedRevision:asset.revision,requestId:requestID()}));
    setSaving(true);
    setMessage('Saving the duplicate choices…');
    try{
      const response=await fetch('/api/decisions/batch',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(changes)});
      if(!response.ok)throw new Error(response.status===409?'One of these files changed. Reload before resolving this group.':'The duplicate choices could not be confirmed.');
      const results:{revision:number}[]=await response.json();
      binChanged();
      group.members.forEach((asset,index)=>patchAsset(asset.id,{status:asset.id===keeperID?'keep':'cull',favourite:asset.id===keeperID&&asset.favourite,revision:results[index].revision}));
      history.record({kind:'decisions',label:`resolved ${group.members.length} copies`,before:group.members.map(snapshot),after:changes.map(change=>({id:change.assetId,status:change.status as Status,favourite:change.favourite}))});
	  setDuplicateGroups(current=>current.filter(item=>item.hash!==group.hash||item.size!==group.size));
      setMessage(`${group.members.length-1} verified ${group.members.length===2?'copy':'copies'} marked for the Bin. No original has moved.`);
    }catch(error){setMessage((error as Error).message)}finally{setSaving(false)}
  }
  // A confirmed choice that a later one on the same file has overtaken only
  // brings its revision: showing it would flip the photo back to an older
  // choice until the later one is confirmed too.
  const queue=useDecisionQueue((job,result,overtaken)=>{
    patchAsset(job.asset.id,overtaken?{revision:result.revision}:{status:job.status,favourite:job.favourite,revision:result.revision});
  });
  /** Saves a choice on a photo, and on the RAW behind it unless `withRAW` is
   * false, which undo uses because its entry names both files already. */
  function save(asset:Asset,status:Status,favourite=asset.favourite,remember=true,withRAW=true){
    if(status==='cull')favourite=false;
    const raw=withRAW?behind.get(asset.id):undefined;
    const files=raw?[asset,raw]:[asset];
    // Both halves go into the queue or neither does, so a pair never splits
    // over a full queue.
    if(files.length>1&&queue.pending+files.length>32){
      setMessage('Choices are waiting to save. Wait for the connection to catch up.');
      return false;
    }
    for(const [index,file] of files.entries()){
      patchAsset(file.id,{status,favourite});
      if(!queue.enqueue({asset:file,status,favourite,wasResolved:file.status==='keep'||file.status==='cull'})){
        files.slice(0,index+1).forEach(done=>patchAsset(done.id,{status:done.status,favourite:done.favourite}));
        setMessage(queue.error||'Review is paused until the pending choice is confirmed.');
        return false;
      }
    }
    if(remember){
      history.record({kind:'decisions',label:`${describe(asset,status,favourite)}${raw?' and its RAW':''}`,before:files.map(snapshot),after:files.map(file=>({id:file.id,status,favourite}))});
    }
    return true;
  }
  /** Splits a pair so the RAW is a photo of its own, or joins it again. */
  async function pairing(raw:number,partner:number,paired:boolean,remember=true){
    const partnerAsset=assets.find(asset=>asset.id===partner);
    const name=partnerAsset?fileName(partnerAsset):'the photo';
    try{
      await setPaired(raw,partner,paired);
      patchAsset(raw,{pair:paired?partner:undefined});
      patchAsset(partner,{pair:paired?raw:undefined});
      if(remember)history.record({kind:'pair',label:paired?`paired ${name} with its RAW`:`unpaired ${name} and its RAW`,raw,partner,before:!paired,after:paired});
      if(remember)setMessage(paired?`${name} and its RAW are one photo again.`:`${name} and its RAW are separate photos now.`);
      return true;
    }catch(error){setMessage((error as Error).message);return false}
  }
  // Undo puts every file of the last action back as it was, through the same
  // queue as any choice, so it is journaled and confirmed the same way. A file
  // already back in that state (undone by hand) is left alone.
  function restore(entry:HistoryEntry,direction:'before'|'after'){
    if(entry.kind==='pair'){
      void pairing(entry.raw,entry.partner,entry[direction],false);
      return true;
    }
    if(entry.kind==='progress'){
      const status=entry[direction];
      if(status==='pending')setCheer(null);
      setSaving(true);
      (async()=>{
        if(status==='done'&&entry.kept){
          // Redo finishes the date again, keeping whatever is undecided.
          applyReviewed(await reviewDate(initial.md));
          return;
        }
        await Promise.all(entry.days.map(day=>setProgress(day,status)));
        setYears(current=>current.map(year=>entry.days.includes(year.day)?{...year,status}:year));
        // Undo also takes back the keeps the mark made, on files nobody has
        // decided differently since.
        const kept=assets.filter(asset=>entry.kept?.includes(asset.id)&&asset.status==='keep');
        if(status==='pending'&&kept.length>0){
          const saved=await sendDecisions(kept.map(asset=>({assetId:asset.id,status:'unreviewed',favourite:asset.favourite,expectedRevision:asset.revision,requestId:requestID()})));
          kept.forEach((asset,index)=>patchAsset(asset.id,{status:'unreviewed',revision:saved[index].revision}));
        }
        binChanged();
      })().catch(error=>setMessage((error as Error).message)).finally(()=>setSaving(false));
      return true;
    }
    for(const target of entry[direction]){
      const asset=assets.find(item=>item.id===target.id);
      if(!asset||(asset.status===target.status&&asset.favourite===target.favourite))continue;
      if(!save(asset,target.status,target.favourite,false,false))return false;
    }
    return true;
  }
  function undo(){
    const taken=history.takeUndo();
    if(!taken){setMessage('Nothing to undo.');return}
    if(restore(taken.entry,'before'))setMessage(`Undone: ${taken.entry.label}.`);else taken.keep();
  }
  function redo(){
    const taken=history.takeRedo();
    if(!taken){setMessage('Nothing to redo.');return}
    if(restore(taken.entry,'after'))setMessage(`Redone: ${taken.entry.label}.`);else taken.keep();
  }
  function applyReviewed(result:DateReviewed){
    const revisions=new Map(result.kept.map(item=>[item.id,item.revision]));
    setYears(current=>current.map(year=>({...year,status:'done',assets:year.assets.map(asset=>revisions.has(asset.id)?{...asset,status:'keep',revision:revisions.get(asset.id)!}:asset)})));
    binChanged();
    return result;
  }
  async function markDate(){
    // A choice still on its way to the server would race the keeps.
    if(saving||queue.pending>0)return;
    setSaving(true);
    try{
      const result=applyReviewed(await reviewDate(initial.md));
      history.record({kind:'progress',label:`marked ${initial.label} reviewed`,days:result.days,before:'pending',after:'done',kept:result.kept.map(item=>item.id)});
      setCheer({label:initial.label,total:result.tally.total,removed:result.tally.removed,bytes:result.tally.bytes,favourites:result.tally.favourites});
    }catch(error){setMessage((error as Error).message)}finally{setSaving(false)}
  }

  // Undo and redo work wherever the day is being reviewed, in the grid or in
  // the viewer, which leaves modifier keys alone.
  useEffect(()=>{
    function key(event:KeyboardEvent){
      const action=historyKey(event);
      if(!action)return;
      event.preventDefault();
      if(event.repeat||saving)return;
      if(action==='undo')undo();else redo();
    }
    window.addEventListener('keydown',key);
    return()=>window.removeEventListener('keydown',key);
  });
  const decideKey=useEffectEvent((asset:Asset,key:string)=>{
    if(key==='x')save(asset,asset.status==='cull'?'unreviewed':'cull');
    if(key==='f')save(asset,asset.status==='cull'?'unreviewed':asset.status,!asset.favourite);
  });
  useEffect(()=>{
    function key(event:KeyboardEvent){
      if(viewer!==null||event.ctrlKey||event.metaKey||event.altKey||event.target instanceof HTMLButtonElement||typing(event.target))return;
      const index=shown.findIndex(asset=>asset.id===selected);
      if(event.key==='ArrowRight'){
        event.preventDefault();
        setSelected(shown[Math.min(shown.length-1,Math.max(0,index+1))]?.id??null);
      }else if(event.key==='ArrowLeft'){
        event.preventDefault();
        setSelected(shown[Math.max(0,index-1)]?.id??null);
      }else if(!event.repeat&&selected!==null){
        const asset=shown.find(item=>item.id===selected);
        if(!asset)return;
        decideKey(asset,event.key.toLowerCase());
      }
    }
    window.addEventListener('keydown',key);
    return()=>window.removeEventListener('keydown',key);
  },[shown,selected,saving,viewer]);

  // Opening a date is seeing what newly arrived on it: the files keep their
  // badge while the page is open, and the date loses its red dot. A report
  // that does not get through is tried again the next time the page loads.
  const seen=useRef(new Set<number>());
  useEffect(()=>{
    const ids=initial.years.flatMap(year=>year.assets.filter(asset=>asset.new&&!seen.current.has(asset.id)).map(asset=>asset.id));
    if(ids.length===0)return;
    ids.forEach(id=>seen.current.add(id));
    fetch('/api/arrivals/seen',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({ids})})
      .then(response=>{if(!response.ok)throw new Error()})
      .catch(()=>ids.forEach(id=>seen.current.delete(id)));
  },[initial]);

  useEffect(()=>{
    const controller=new AbortController();
    fetch(`/api/duplicates?md=${initial.md}`,{signal:controller.signal}).then(async response=>{
      if(!response.ok)throw new Error('Could not load verified duplicates.');
      const groups:DuplicateGroup[]=await response.json();
      setDuplicateGroups(groups);
      setKeepers(Object.fromEntries(groups.map(group=>[group.hash,group.members[0].id])));
    }).catch(error=>{if(!controller.signal.aborted)setMessage((error as Error).message)});
    return()=>controller.abort();
  },[initial.md]);

  // The date lives in the search bar as a pill; a single day's page names its
  // year too.
  const dayPage=location.pathname.match(/^\/day\/(\d{4}-\d{2}-\d{2})/)?.[1];
  const pageLabel=dayPage?dayName(`/day/${dayPage}`):initial.label;
  usePageDate({md:initial.md,label:pageLabel,
    short:shortLabel(initial.md,dayPage?.slice(0,4)),
    done:dateDone,years:years.length,yearsDone:doneYears,previous:initial.previous,next:initial.next});
  // A link to a RAW that shows behind its JPEG opens the pair.
  const viewing=viewer===null?null:[...behind].find(([,raw])=>raw.id===viewer)?.[0]??viewer;
  // The viewer walks the files the grid showed when it opened. A choice that
  // takes a file out of the filter keeps it in the walk until the viewer
  // closes, so the next file is the grid's next, and nothing the filter hid
  // (a file removed earlier, say) ever comes up.
  const walk=useRef<ReadonlySet<number>|'all'|null>(null);
  if(viewing===null)walk.current=null;
  else if(walk.current===null)walk.current=filters.size>0&&shownIDs.has(viewing)?shownIDs:'all';
  const frozen=walk.current;
  const walked=useMemo(()=>frozen===null||frozen==='all'?tiles:tiles.filter(asset=>frozen.has(asset.id)),[tiles,frozen]);
  usePageFilters(assets.length>0?{
    options:filterChips.map(chip=>({...chip,on:filters.has(chip.id),count:tiles.filter(asset=>matches(asset,new Set([chip.id]))).length})),
    toggle:id=>toggleFilter(id as Filter),clear:()=>saveFilters(new Set()),
    sort:years.length>1?{options:orders,value:order,set:saveOrder}:undefined,
  }:null);
  usePageActions(assets.length>0&&doneYears<years.length?{actions:[{label:`Mark ${initial.label} reviewed`,short:'Mark reviewed',icon:'task_alt',disabled:saving||queue.pending>0,onClick:()=>void markDate()}]}:null);
  return <>
    <h1 className="vh">{pageLabel}</h1>
    <div className="dline">
      <span className="count">{(initial.memories-hidden.size).toLocaleString()} {initial.memories-hidden.size===1?'memory':'memories'}</span>
      <span className="sep">·</span><span>{years.length} {years.length===1?'year':'years'}</span>
      <span className="sep">·</span><span className="dim">{bytes(initial.bytes)}</span>
    </div>
    {assets.length>0&&shown.length===0&&<p className="note">Nothing on this date matches the filters. <button type="button" className="textbtn" onClick={()=>saveFilters(new Set())}>Clear filters</button></p>}
    {queue.error&&<p className="note warn" role="alert">{queue.error} <button className="btn small" onClick={queue.retry}>Retry the same save</button></p>}
    {years.length===0&&<p className="note">Nothing in the archive is filed under {initial.label}, so there is nothing to review.</p>}
    {duplicateGroups.length>0&&<section className="xdupes">
      <h2>Same file, different folders <small>{duplicateGroups.length} {duplicateGroups.length===1?'group':'groups'} · byte-identical, verified by full hash</small></h2>
      {duplicateGroups.map(group=><div className="xgroup" key={`${group.hash}:${group.size}`}>
        <p className="xmeta">{group.members.length} identical copies · {bytes(group.size)} each · <strong>{bytes(group.reclaimable)}</strong> reclaimable</p>
        <div className="gal tight">{group.members.map(member=><figure className={`mo${(keepers[group.hash]??group.members[0].id)===member.id?' keeper':''}`} key={member.id}>
          <Media asset={member}/>
          <div className="bdg"><button type="button" className="b tocmp" disabled={saving} onClick={()=>setKeepers(current=>({...current,[group.hash]:member.id}))}>{(keepers[group.hash]??group.members[0].id)===member.id?'keep this one':'choose as keeper'}</button></div>
          <figcaption className="cap"><span>{member.day}</span></figcaption>
        </figure>)}</div>
        <p className="xact"><button type="button" className="btn small danger" disabled={saving} onClick={()=>void resolveGroup(group)}>Keep the selected copy, mark the other {group.members.length-1} for the Bin</button><span className="hint">Nothing is deleted. The Bin remains separately reviewable and restorable.</span></p>
      </div>)}
    </section>}
    {shownYears.filter(year=>filters.size===0||year.assets.some(asset=>shownIDs.has(asset.id))).map(year=>{
      const memories=year.assets.filter(asset=>!hidden.has(asset.id)).length;
      return <section className={`yr${year.status==='done'?' settled':''}`} key={year.day}>
      <div className="yhead"><h2>{year.year}{year.status==='done'&&<span className="tag done">reviewed</span>}{(year.fresh??0)>0&&<span className="tag fresh" title="Reached the archive since this date was last opened"><span className="freshdot" aria-hidden="true"/>{year.fresh!.toLocaleString()} new</span>}</h2><p className="ymeta"><span>{memories.toLocaleString()} {memories===1?'memory':'memories'}{memories!==year.files&&<span className="dim"> from {year.files.toLocaleString()} files</span>}</span><span className="dim">{bytes(year.bytes)}</span></p></div>
      {year.assets.length===0?<p className="note">Nothing left in this folder.</p>:<YearGrid assets={year.assets.filter(asset=>shownIDs.has(asset.id))} render={(asset,box)=>
        // oxlint-disable-next-line jsx-a11y/no-noninteractive-element-interactions, jsx-a11y/no-noninteractive-tabindex -- a tile holds its own buttons, so it cannot be one; it is the grid's focus stop and opens on Enter
        <figure tabIndex={0} className={`mo${asset.favourite?' fav':''}${asset.status!=='unreviewed'?' seen':''}${asset.status==='cull'?' culled':''}${selected===asset.id?' sel':''}`} key={asset.id} data-asset={asset.id} {...shapeProps({key:asset.id,ratio:shapeOf(asset)},box)} onClick={()=>{setSelected(asset.id);photo.show(asset.id)}}
        onKeyDown={event=>{if(event.target===event.currentTarget&&(event.key==='Enter'||event.key===' ')){event.preventDefault();setSelected(asset.id);photo.show(asset.id)}}}>
        <Media asset={asset}/>
        <div className="bdg">{asset.new&&asset.status==='unreviewed'&&<span className="b new">new</span>}{behind.has(asset.id)&&<span className="b pair">{pairLabel(asset)}</span>}{(asset.relatedCount??0)>(behind.has(asset.id)?1:0)&&<span className="b dupe">duplicate</span>}</div>
        <div className="acts"><button type="button" className="act cull" disabled={!queue.ready} onClick={event=>{event.stopPropagation();save(asset,asset.status==='cull'?'unreviewed':'cull')}}><Icon name="delete"/><span className="actlabel">{asset.status==='cull'?'Undo':'Remove'}</span></button><button type="button" className="act fav" disabled={!queue.ready} aria-pressed={asset.favourite} onClick={event=>{event.stopPropagation();save(asset,asset.status==='cull'?'unreviewed':asset.status,!asset.favourite)}}>♡</button></div>
        {captureTime(asset.capturedAt)&&<div className="when">{captureTime(asset.capturedAt)}</div>}
        {(asset.duration||asset.kind==='video')&&<span className="dur" aria-label={asset.duration?`Video, ${runningTime(asset.duration)}`:'Video'}>{asset.duration?runningTime(asset.duration):<Icon name="play_circle" filled/>}</span>}
        {asset.status==='cull'&&<div className="undo"><span>Removed</span><button type="button" className="act" disabled={!queue.ready} onClick={event=>{event.stopPropagation();save(asset,'unreviewed')}}>Undo</button></div>}
      </figure>}/>}
    </section>})}
    {(message||(tip&&assets.length>0))&&<Snacks>
      {message&&<div className="snack" role="status">{saving?<Busy label={message} state="working"/>:message}</div>}
      {tip&&assets.length>0&&<div className="snack" role="status">Click any photo to review. <b>→</b> next, <b>k</b> keep, <b>x</b> remove, <b>f</b> favourite, <b>{undoKeys.undo}</b> undo, <b>{undoKeys.redo}</b> redo, <b>?</b> for the rest</div>}
    </Snacks>}
    {cheer&&<Celebration tally={cheer} nextHref={`/on/${initial.next}`} nextLabel={calendarLabel(initial.next)} onClose={()=>setCheer(null)}/>}
    {viewing!==null&&<Viewer assets={walked} initialID={viewing} onClose={photo.close} onMove={photo.moved} onSave={save} onPatch={patchAsset} onRecord={history.record}
      rawOf={asset=>behind.get(asset.id)} onUnpair={(still,raw)=>void pairing(raw.id,still.id,false)}/>}
  </>;
}
