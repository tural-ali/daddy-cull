import {useCallback,useEffect,useEffectEvent,useMemo,useRef,useState,type ReactNode} from 'react';
import {binChanged,type Asset,type Status} from '../api';
import {Media} from '../Media';
import {useDecisionQueue} from '../useDecisionQueue';
import {calendarLabel} from './Year';
import {Viewer} from './Viewer';
import {pick} from './Duplicates';
import {usePhotoURL} from './photoURL';
import {Busy} from '../Busy';
import {Icon,type IconName} from '../Icon';
import {Celebration,type Tally} from './Celebration';
import {usePageActions} from './pageActions';
import {shortLabel,usePageDate} from './DatePicker';
import {dayName} from './goto';
import {requestID,sendDecisions} from './decisions';
import {historyKey,undoKeys,useHistory,type HistoryEntry,type Snapshot} from './history';
import {CopyGroup} from './CopyGroup';
import {fileFormat,setPaired,stackFormats,stackOf,stacksBehind} from './stacks';
import {usePageFilters,type SortOption} from './SearchFilters';
import {Snacks} from './Snacks';
import {flyToBin} from './binFlight';
import {Justified,shapeOf,shapeProps,type Box} from './justified';
import {Pick,usePicks,useSelectionBar} from './selection';
import {tracked} from '../saving';

// fresh counts files that reached the archive after the day was reviewed and
// still wait; each carries new.
export type TodayYear={day:string;year:number;files:number;bytes:number;status:'pending'|'done';assets:Asset[];fresh?:number};
export type TodayData={md:string;label:string;previous:string;next:string;years:TodayYear[];memories:number;bytes:number};
// located is only read for videos compared by their footage. sidecars is what
// the copy's own sidecars record, the ones the Bin takes with it; it is absent
// when they were not read, which is not the same as none.
export type SidecarFacts={files:number;people:number;keywords:number;rating:number;captioned:boolean};
export type DuplicateMember=Asset&{day:string;located:boolean;sidecars?:SidecarFacts};
// bytes: byte-identical on a full hash. footage: videos whose pictures and
// sound are identical, whose metadata differs.
export type DuplicateGroup={hash:string;proof:'bytes'|'footage';size:number;reclaimable:number;members:DuplicateMember[]};

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

function snapshot(asset:Asset):Snapshot{return {id:asset.id,status:asset.status,favourite:asset.favourite}}
/** A photo's tile on the date's grid, if it is drawn. */
function tileOf(id:number){return document.querySelector(`main figure.mo[data-asset="${id}"]`)}

function fileName(asset:Asset){return asset.path.split('/').pop()??''}
/** " and its RAW", or " and its RAW and HEIC", for a choice on a stack. */
function alongside(files:Asset[]){return files.length>1?` and its ${files.slice(1).map(fileFormat).join(' and ')}`:''}
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
  const items=useMemo(()=>assets.map(asset=>({key:asset.id,ratio:shapeOf(asset),turn:asset.turn})),[assets]);
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
  // A RAW and its exports are one tile, an export; the other files ride
  // along with every choice and show in the viewer on request.
  const behind=useMemo(()=>stacksBehind(assets),[assets]);
  const hidden=useMemo(()=>new Set([...behind.values()].flat().map(file=>file.id)),[behind]);
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
      flyToBin(group.members.filter(asset=>asset.id!==keeperID).map(asset=>document.querySelector(`.xgroup tr[data-asset="${asset.id}"]`)));
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
  /** Saves a choice on a photo, and on the files behind it unless `withStack`
   * is false, which undo uses because its entry names every file already. */
  function save(asset:Asset,status:Status,favourite=asset.favourite,remember=true,withStack=true){
    if(status==='cull')favourite=false;
    const files=withStack?stackOf(asset,behind):[asset];
    // Every file of a stack goes into the queue or none does, so a stack
    // never splits over a full queue.
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
    if(remember&&status==='cull'&&asset.status!=='cull')flyToBin([tileOf(asset.id)]);
    if(remember){
      history.record({kind:'decisions',label:`${describe(asset,status,favourite)}${alongside(files)}`,before:files.map(snapshot),after:files.map(file=>({id:file.id,status,favourite}))});
    }
    return true;
  }
  /** Saves one choice on many photos at once, a selection, with their stacks,
   * as one step to undo. It goes as batches rather than through the queue,
   * which holds a few choices at a time. */
  async function saveMany(picked:Asset[],decide:(asset:Asset)=>Snapshot,label:string,remember=true){
    if(saving)return false;
    if(queue.pending>0){setMessage('Choices are still saving. Try again in a moment.');return false}
    const targets=picked.flatMap(asset=>{
      const {status,favourite}=decide(asset);
      return stackOf(asset,behind).map(file=>({file,status,favourite:status==='cull'?false:favourite}));
    }).filter(target=>target.file.status!==target.status||target.file.favourite!==target.favourite);
    if(targets.length===0)return true;
    setSaving(true);
    if(remember)flyToBin(targets.filter(target=>target.status==='cull'&&target.file.status!=='cull').map(target=>tileOf(target.file.id)));
    targets.forEach(target=>patchAsset(target.file.id,{status:target.status,favourite:target.favourite}));
    try{
      const saved=await sendDecisions(targets.map(target=>({assetId:target.file.id,status:target.status,favourite:target.favourite,expectedRevision:target.file.revision,requestId:requestID()})));
      targets.forEach((target,index)=>patchAsset(target.file.id,{revision:saved[index].revision}));
      if(remember)history.record({kind:'decisions',label,before:targets.map(target=>snapshot(target.file)),after:targets.map(target=>({id:target.file.id,status:target.status,favourite:target.favourite}))});
      return true;
    }catch(error){
      targets.forEach(target=>patchAsset(target.file.id,{status:target.file.status,favourite:target.file.favourite}));
      setMessage((error as Error).message);
      return false;
    }finally{setSaving(false)}
  }
  // Turns are sent one after another, so they land in the order pressed, and
  // the server's answer is only shown once the last is in, so a quick second
  // press never flicks back.
  const turning=useRef<Promise<unknown>>(Promise.resolve());
  const turnsOut=useRef(0);
  /** Turns photos a quarter at a time, with their stacks, in Cull only. */
  function turn(picked:Asset[],quarters:number,remember=true){
    const files=picked.flatMap(asset=>stackOf(asset,behind));
    if(files.length===0)return;
    files.forEach(file=>patchAsset(file.id,{turn:(((file.turn??0)+quarters)%4+4)%4||undefined}));
    if(remember)history.record({kind:'turn',label:`turned ${picked.length===1?fileName(picked[0]):`${picked.length.toLocaleString()} photos`}`,ids:files.map(file=>file.id),quarters});
    turnsOut.current++;
    const send=async()=>{
      const response=await fetch('/api/turns',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({ids:files.map(file=>file.id),quarters})});
      if(!response.ok)throw new Error('The turn could not be saved. Reload to see how the photos are shown.');
      return (await response.json() as {turns:Record<string,number>}).turns;
    };
    const run=turning.current.then(send);
    turning.current=tracked(run.then(turns=>{
      if(--turnsOut.current===0)files.forEach(file=>patchAsset(file.id,{turn:turns[String(file.id)]||undefined}));
    },error=>{
      turnsOut.current--;
      files.forEach(file=>patchAsset(file.id,{turn:file.turn}));
      setMessage((error as Error).message);
    }));
  }
  /** Shows a RAW and its exports as separate photos, or as one again. */
  async function pairing(raw:number,partners:number[],paired:boolean,remember=true){
    const files=[raw,...partners];
    const face=assets.find(asset=>asset.id===partners[0]);
    const name=face?fileName(face):'the photo';
    try{
      await Promise.all(partners.map(partner=>setPaired(raw,partner,paired)));
      // The RAW keeps the exports not named, as the server does, so taking
      // one export out leaves the others stacked with it.
      const members=new Set([raw,...assets.find(asset=>asset.id===raw)?.stack??[]]);
      partners.forEach(partner=>{if(paired)members.add(partner);else members.delete(partner)});
      if(members.size===1)members.clear();
      members.forEach(id=>patchAsset(id,{stack:[...members].filter(other=>other!==id)}));
      files.filter(id=>!members.has(id)).forEach(id=>patchAsset(id,{stack:undefined}));
      if(remember)history.record({kind:'pair',label:paired?`stacked ${name} with its RAW`:`separated ${name} from its RAW`,raw,partners,before:!paired,after:paired});
      if(remember)setMessage(paired?`${name} and its RAW are one photo again.`:`${name} and its RAW are separate photos now.`);
      return true;
    }catch(error){setMessage((error as Error).message);return false}
  }
  /** Removes one file of a stack and leaves the rest as they are: the file
   * leaves the stack and goes to the Bin. A RAW takes its exports' stack with
   * it, since a stack is a RAW and what was exported from it. */
  async function removeFormat(face:Asset,file:Asset,remember=true){
    const files=stackOf(face,behind);
    const raw=files.find(item=>item.kind==='raw');
    if(!raw||!files.includes(file))return false;
    const partners=file===raw?files.filter(item=>item!==raw).map(item=>item.id):[file.id];
    if(!await pairing(raw.id,partners,false,false))return false;
    if(!save(file,'cull',false,false,false)){void pairing(raw.id,partners,true,false);return false}
    const rest=files.filter(item=>item!==file);
    if(remember){
      history.record({kind:'format',label:`removed only the ${fileFormat(file)} of ${fileName(face)}`,raw:raw.id,partners,file:snapshot(file)});
      setMessage(`Marked ${fileName(file)} for the Bin. ${file===raw&&rest.length>1?`The ${rest.map(fileFormat).join(' and ')} stay, as separate photos now.`:`The ${rest.map(fileFormat).join(' and ')} ${rest.length===1?'stays':'stay'}.`}`);
    }
    return true;
  }
  // Undo puts every file of the last action back as it was, through the same
  // queue as any choice, so it is journaled and confirmed the same way. A file
  // already back in that state (undone by hand) is left alone.
  function restore(entry:HistoryEntry,direction:'before'|'after'){
    if(entry.kind==='turn'){
      const files=assets.filter(asset=>entry.ids.includes(asset.id)&&!hidden.has(asset.id));
      const lone=assets.filter(asset=>entry.ids.includes(asset.id)&&hidden.has(asset.id)&&!files.some(still=>behind.get(still.id)?.some(file=>file.id===asset.id)));
      turn([...files,...lone],direction==='before'?-entry.quarters:entry.quarters,false);
      return true;
    }
    if(entry.kind==='pair'){
      void pairing(entry.raw,entry.partners,entry[direction],false);
      return true;
    }
    if(entry.kind==='format'){
      const file=assets.find(asset=>asset.id===entry.file.id);
      if(!file)return false;
      if(direction==='after'){
        void pairing(entry.raw,entry.partners,false,false).then(split=>{if(split)save(file,'cull',false,false,false)});
        return true;
      }
      if(!save(file,entry.file.status,entry.file.favourite,false,false))return false;
      void pairing(entry.raw,entry.partners,true,false);
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
    // A choice on more than one photo, from a selection, goes back the way it
    // came, as a batch. Each stack rides with its photo, as it did when the
    // choice was made, so a photo and the files behind it count as one.
    const targets=new Map(entry[direction].map(target=>[target.id,target]));
    const photos=assets.filter(asset=>targets.has(asset.id)&&!hidden.has(asset.id));
    if(photos.length>1){
      if(saving||queue.pending>0)return false;
      void saveMany(photos,asset=>targets.get(asset.id)!,entry.label,false);
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
  const held=useRef<'undo'|'redo'|null>(null);
  const runHeld=useEffectEvent(()=>{
    const action=held.current;
    held.current=null;
    if(action==='undo')undo();else if(action==='redo')redo();
  });
  useEffect(()=>{if(!saving&&held.current)runHeld()},[saving]);
  useEffect(()=>{
    function key(event:KeyboardEvent){
      const action=historyKey(event);
      if(!action)return;
      event.preventDefault();
      if(event.repeat)return;
      // Pressed while a choice is still being confirmed, it waits for it,
      // rather than being lost.
      if(saving){held.current=action;return}
      if(action==='undo')undo();else redo();
    }
    window.addEventListener('keydown',key);
    return()=>window.removeEventListener('keydown',key);
  });
  const picks=usePicks(shown,asset=>asset.id);
  const place=useMemo(()=>new Map(shown.map((asset,index)=>[asset.id,index])),[shown]);
  const chosen=shown.filter(asset=>picks.picked.has(asset.id));
  const picking=chosen.length>0;
  const decideKey=useEffectEvent((asset:Asset,key:string)=>{
    if(key==='x')save(asset,asset.status==='cull'?'unreviewed':'cull');
    if(key==='f')save(asset,asset.status==='cull'?'unreviewed':asset.status,!asset.favourite);
  });
  useEffect(()=>{
    function key(event:KeyboardEvent){
      // A copy opened full size from a duplicate group has the keys to itself.
      if(viewer!==null||document.documentElement.classList.contains('rv-open')||event.ctrlKey||event.metaKey||event.altKey||event.target instanceof HTMLButtonElement||typing(event.target))return;
      // A selection's keys are the selection bar's.
      if(picking&&event.key!=='ArrowRight'&&event.key!=='ArrowLeft')return;
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
  },[shown,selected,saving,viewer,picking]);

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
      setKeepers(Object.fromEntries(groups.map(group=>[group.hash,pick(group.members,'clean').id])));
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
  // A link to a file that shows behind another opens the photo of its stack.
  const photoOf=(id:number)=>[...behind].find(([,files])=>files.some(file=>file.id===id))?.[0]??id;
  const viewing=viewer===null?null:photoOf(viewer);
  // Opened from a filtered grid, the viewer walks the files the filter shows,
  // as the grid does: a choice that takes a file out of the filter takes it
  // out of the walk too, once the viewer has moved off it, and nothing the
  // filter hid (a file removed earlier, say) ever comes up. The file on screen
  // stays until then, so a choice that does not move on leaves it in view.
  const filtering=useRef<boolean|null>(null);
  if(viewing===null)filtering.current=null;
  else if(filtering.current===null)filtering.current=filters.size>0&&shownIDs.has(viewing);
  const filtered=filtering.current===true;
  // Which file that is: the address follows the viewer without the page
  // hearing of it, so a filtered walk keeps its own note.
  const [onScreen,setOnScreen]=useState<number|null>(null);
  if(viewing===null&&onScreen!==null)setOnScreen(null);
  const addressMoved=photo.moved;
  const moved=useCallback((id:number)=>{if(filtering.current)setOnScreen(id);addressMoved(id)},[addressMoved]);
  const inView=onScreen===null?viewing:photoOf(onScreen);
  const walked=useMemo(()=>filtered?tiles.filter(asset=>asset.id===inView||shownIDs.has(asset.id)):tiles,[tiles,filtered,inView,shownIDs]);
  usePageFilters(assets.length>0?{
    options:filterChips.map(chip=>({...chip,on:filters.has(chip.id),count:tiles.filter(asset=>matches(asset,new Set([chip.id]))).length})),
    toggle:id=>toggleFilter(id as Filter),clear:()=>saveFilters(new Set()),
    sort:years.length>1?{options:orders,value:order,set:saveOrder}:undefined,
  }:null);
  // Many photos at once, as in Google Photos: the tick on a tile's corner
  // starts a selection, and while there is one a click on a tile adds it or
  // takes it out rather than opening it.
  const allShown=shown.length>0&&chosen.length===shown.length;
  const allFavourite=picking&&chosen.every(asset=>asset.favourite);
  const allRemoved=picking&&chosen.every(asset=>asset.status==='cull');
  const many=chosen.length===1?fileName(chosen[0]):`${chosen.length.toLocaleString()} photos`;
  useSelectionBar({count:chosen.length,busy:saving,clear:picks.clear,actions:[
    {label:allShown?'Deselect all':`Select all ${shown.length.toLocaleString()}`,icon:'select_all',keys:'Mod+A',onClick:()=>allShown?picks.clear():picks.all()},
    {label:'Rotate clockwise',icon:'rotate_right',keys:']',onClick:()=>turn(chosen,1)},
    {label:allFavourite?'Remove from favourites':'Favourite',icon:'favorite',filled:allFavourite,keys:'F',
      onClick:()=>void saveMany(chosen,asset=>({id:asset.id,status:asset.status==='cull'?'unreviewed':asset.status,favourite:!allFavourite}),`${allFavourite?'unfavourited':'favourited'} ${many}`)},
    {label:allRemoved?'Undo remove':'Remove',icon:allRemoved?'restore_from_trash':'delete',keys:'X',danger:!allRemoved,
      onClick:()=>void saveMany(chosen,asset=>({id:asset.id,status:allRemoved?'unreviewed':'cull',favourite:asset.favourite}),`${allRemoved?'put back':'removed'} ${many}`).then(done=>{if(done&&!allRemoved)picks.clear()})},
  ]});
  function openOrPick(asset:Asset,extend:boolean){
    if(picks.tap(place.get(asset.id)??0,extend))return;
    setSelected(asset.id);photo.show(asset.id);
  }
  usePageActions(assets.length>0&&doneYears<years.length?{actions:[{label:`Mark ${initial.label} reviewed`,short:'Mark reviewed',icon:'check',keys:'Shift+R',disabled:saving||queue.pending>0,onClick:()=>void markDate()}]}:null);
  return <>
    <section className="dupehead dayhead">
      <h1>{pageLabel}</h1>
      <p className="ysum dline">
        <span className="count"><b>{(initial.memories-hidden.size).toLocaleString()}</b> {initial.memories-hidden.size===1?'memory':'memories'}</span>
        <span className="sep">·</span><span><b>{years.length}</b> {years.length===1?'year':'years'}</span>
        <span className="sep">·</span><span>{bytes(initial.bytes)}</span>
      </p>
    </section>
    {assets.length>0&&shown.length===0&&<p className="note">Nothing on this date matches the filters. <button type="button" className="textbtn" onClick={()=>saveFilters(new Set())}>Clear filters</button></p>}
    {queue.error&&<p className="note warn" role="alert">{queue.error} <button className="btn small" onClick={queue.retry}>Retry the same save</button></p>}
    {years.length===0&&<p className="note">Nothing in the archive is filed under {initial.label}, so there is nothing to review.</p>}
    {duplicateGroups.length>0&&<section className="xdupes">
      {duplicateGroups.every(group=>group.proof!=='footage')
        ?<h2>Same file, different folders <small>{duplicateGroups.length} {duplicateGroups.length===1?'group':'groups'} · byte-identical, verified by full hash</small></h2>
        :<h2>Copies <small>{duplicateGroups.length} {duplicateGroups.length===1?'group':'groups'} · verified by a full hash of each file, or of each video's footage</small></h2>}
      {duplicateGroups.map(group=><CopyGroup key={`${group.hash}:${group.size}`} group={group} keeperID={keepers[group.hash]??group.members[0].id} saving={saving} onChoose={id=>setKeepers(current=>({...current,[group.hash]:id}))} onResolve={()=>void resolveGroup(group)}/>)}
    </section>}
    {shownYears.filter(year=>filters.size===0||year.assets.some(asset=>shownIDs.has(asset.id))).map(year=>{
      const memories=year.assets.filter(asset=>!hidden.has(asset.id)).length;
      return <section className={`yr${year.status==='done'?' settled':''}`} key={year.day}>
      <div className="yhead"><h2>{year.year}{year.status==='done'&&<span className="tag done">reviewed</span>}{(year.fresh??0)>0&&<span className="tag fresh" title="Reached the archive since this date was last opened"><span className="freshdot" aria-hidden="true"/>{year.fresh!.toLocaleString()} new</span>}</h2><p className="ymeta"><span>{memories.toLocaleString()} {memories===1?'memory':'memories'}{memories!==year.files&&<span className="dim"> from {year.files.toLocaleString()} files</span>}</span><span className="dim">{bytes(year.bytes)}</span></p></div>
      {year.assets.length===0?<p className="note">Nothing left in this folder.</p>:<YearGrid assets={year.assets.filter(asset=>shownIDs.has(asset.id))} render={(asset,box)=>
        // oxlint-disable-next-line jsx-a11y/no-noninteractive-element-interactions, jsx-a11y/no-noninteractive-tabindex -- a tile holds its own buttons, so it cannot be one; it is the grid's focus stop and opens on Enter
        <figure tabIndex={0} className={`mo${asset.favourite?' fav':''}${asset.status!=='unreviewed'?' seen':''}${asset.status==='cull'?' culled':''}${selected===asset.id?' sel':''}${picks.picked.has(asset.id)?' picked':''}`} key={asset.id} data-asset={asset.id} {...shapeProps({key:asset.id,ratio:shapeOf(asset),turn:asset.turn},box)} onClick={event=>openOrPick(asset,event.shiftKey)}
        onFocus={event=>{if(event.target===event.currentTarget)setSelected(asset.id)}}
        onKeyDown={event=>{if(event.target===event.currentTarget&&(event.key==='Enter'||event.key===' ')){event.preventDefault();openOrPick(asset,event.shiftKey)}}}>
        <Media asset={asset}/>
        <Pick checked={picks.picked.has(asset.id)} label={`Select ${fileName(asset)}`} onToggle={extend=>picks.toggle(place.get(asset.id)??0,extend)}/>
        <div className="bdg end">{asset.new&&asset.status==='unreviewed'&&<span className="b new">new</span>}{behind.has(asset.id)&&stackFormats(stackOf(asset,behind)).map(format=><span className="b pair" key={format}>{format}</span>)}</div>
        <button type="button" className="tfav" disabled={!queue.ready} aria-pressed={asset.favourite} aria-label={asset.favourite?'Remove from favourites':'Favourite'} onClick={event=>{event.stopPropagation();save(asset,asset.status==='cull'?'unreviewed':asset.status,!asset.favourite)}}><Icon name="favorite" filled={asset.favourite}/></button>
        {(asset.duration||asset.kind==='video')&&<span className="dur" aria-label={asset.duration?`Video, ${runningTime(asset.duration)}`:'Video'}>{asset.duration?runningTime(asset.duration):<Icon name="play_circle" filled/>}</span>}
        {asset.status==='cull'&&<div className="undo"><span>Removed</span><button type="button" className="act" disabled={!queue.ready} onClick={event=>{event.stopPropagation();save(asset,'unreviewed')}}>Undo</button></div>}
      </figure>}/>}
    </section>})}
    {(message||(tip&&assets.length>0&&viewing===null))&&<Snacks>
      {message&&<div className="snack" role="status">{saving?<Busy label={message} state="working"/>:message}</div>}
      {tip&&assets.length>0&&viewing===null&&<div className="snack" role="status">Click any photo to review. <b>→</b> next, <b>k</b> keep, <b>x</b> remove, <b>f</b> favourite, <b>{undoKeys.undo}</b> undo, <b>{undoKeys.redo}</b> redo, <b>?</b> for the rest</div>}
    </Snacks>}
    {cheer&&<Celebration tally={cheer} nextHref={`/on/${initial.next}`} nextLabel={calendarLabel(initial.next)} onClose={()=>setCheer(null)}/>}
    {viewing!==null&&<Viewer assets={walked} initialID={viewing} onClose={photo.close} onMove={moved} onSave={save} onPatch={patchAsset} onRecord={history.record} onTurn={(asset,quarters)=>turn([asset],quarters)}
      behindOf={asset=>behind.get(asset.id)} onRemoveFormat={(still,file)=>void removeFormat(still,file)} onSeparate={(still,files)=>{const raw=files.find(file=>file.kind==='raw');if(raw)void pairing(raw.id,[still,...files].filter(file=>file!==raw).map(file=>file.id),false)}}/>}
  </>;
}
