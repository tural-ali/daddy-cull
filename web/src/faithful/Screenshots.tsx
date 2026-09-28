import {Media} from '../Media';
import {type Asset,type Status} from '../api';
import {Icon} from '../Icon';
import {Viewer} from './Viewer';
import {useEffect,useRef,useState} from 'react';
import {Pick,RowTile,Rows,useSelectionBar,usePicks,type SelectionAction} from './selection';
import {usePageFilters} from './SearchFilters';
import {MoreMarker,useMoreOnScroll} from './more';
import {shapeOf} from './justified';
import {usePhotoURL} from './photoURL';
import {requestID,sendDecisions} from './decisions';
import {undoableDecisions,usePageUndo,type Undoable} from './pageUndo';
import {tipProps} from './keys';
import {undoKeys} from './history';
import {flyToBin} from './binFlight';
import {TASK_FINISHED,queueScreenshots,taskAction,waitForTask,type Task} from './taskQueue';

type ScreenshotItem=Asset&{day:string;name:string;state:string};
export type ScreenshotPage={items:ScreenshotItem[];total:number;bytes:number;unreviewed:number;reviewed:number;stills:number;recordings:number};
function bytes(value:number){return value<1024**2?`${(value/1024).toFixed(1)} KB`:value<1024**3?`${(value/1024**2).toFixed(1)} MB`:`${(value/1024**3).toFixed(1)} GB`}
function plural(count:number,one:string){return `${count.toLocaleString()} ${one}${count===1?'':'s'}`}

// The filters live in the search bar: what has not been looked at yet and
// what has been kept, as in Lightroom, and stills or screen recordings. Two
// from one group show both, as none does. Keeping is a decision only; the
// file stays where it is. The page opens on what is not reviewed.
type FilterID='unreviewed'|'reviewed'|'image'|'video';
const FILTERS:{id:FilterID;label:string;icon:'schedule'|'check_circle'|'image'|'videocam';group:string;words:string[];count:(page:ScreenshotPage)=>number}[]=[
  {id:'unreviewed',label:'Not reviewed',icon:'schedule',group:'Review',words:['not reviewed','unreviewed','to review','waiting'],count:page=>page.unreviewed},
  {id:'reviewed',label:'Reviewed',icon:'check_circle',group:'Review',words:['reviewed','kept','keep'],count:page=>page.reviewed},
  {id:'image',label:'Stills',icon:'image',group:'Type',words:['stills','images','pictures','photos'],count:page=>page.stills},
  {id:'video',label:'Recordings',icon:'videocam',group:'Type',words:['recordings','videos','screen recordings'],count:page=>page.recordings},
];
function reviewFor(on:ReadonlySet<FilterID>){return on.has('unreviewed')===on.has('reviewed')?'all':on.has('reviewed')?'reviewed':''}
function kindFor(on:ReadonlySet<FilterID>){return on.has('image')===on.has('video')?'':on.has('image')?'image':'video'}
function filtersFrom(review:string,kind:string){
  const on=new Set<FilterID>();
  if(review==='')on.add('unreviewed');
  if(review==='reviewed')on.add('reviewed');
  if(kind==='image'||kind==='video')on.add(kind);
  return on;
}
function query(review:string,kind:string,from:number){return `/api/screenshots?kind=${encodeURIComponent(kind)}&review=${review}&from=${from}`}

/** Moving screenshots to the Bin as an undoable step. The move runs in the
 * background as a task; undo stops it if it is still going and waits while
 * every screenshot it moved comes back, last first, and redo queues them
 * again. */
function undoableRemove(label:string,task:Task,ids:number[]):Undoable{
  let current=task;
  return {label,
    undo:async()=>{
      const back=await waitForTask((await taskAction(current.id,'undo')).id);
      if(back.state==='failed')throw new Error(`${plural(back.failed,'screenshot')} could not be brought back from the Bin. Tasks says which.`);
    },
    redo:async()=>{current=await queueScreenshots(ids,'remove')}};
}

export function Screenshots({page,filter:initialKind,review:initialReview}:{page:ScreenshotPage;filter:string;review:string}){
  const [current,setCurrent]=useState(page);
  // The filters as switched, which the search bar shows at once, and the
  // filters of the list on the page, which follow when their list lands.
  const [on,setOn]=useState<ReadonlySet<FilterID>>(()=>filtersFrom(initialReview,initialKind));
  const [listed,setListed]=useState(()=>({review:reviewFor(on),kind:kindFor(on)}));
  const {review,kind:filter}=listed;
  // Bumped whenever the list changes under a read in flight, which then
  // lands nowhere: its offset no longer points where it did.
  const version=useRef(0);
  const [busy,setBusy]=useState(false);
  const [message,setMessage]=useState('');
  const [error,setError]=useState('');
  const {items,total,bytes:totalBytes}=current;
  const picks=usePicks(items,item=>item.id);
  const steps=usePageUndo(busy,setError);
  const photo=usePhotoURL(id=>page.items.some(item=>String(item.id)===id));

  // Read at the length loaded: what was decided has already left the list
  // here and on the server, so the offsets agree.
  const more=useMoreOnScroll(`${review}/${filter}`,items.length<total,items.length,async()=>{
    const asked=version.current;
    const response=await fetch(query(review,filter,items.length));
    if(!response.ok)throw new Error('more');
    const next:ScreenshotPage=await response.json();
    if(asked!==version.current)return;
    setCurrent(value=>{
      const have=new Set(value.items.map(item=>item.id));
      return {...next,items:[...value.items,...next.items.filter(item=>!have.has(item.id))]};
    });
  });
  async function choose(next:ReadonlySet<FilterID>){
    setOn(next);
    const nextReview=reviewFor(next),nextKind=kindFor(next);
    const address=new URL(location.href);
    address.search=new URLSearchParams({review:nextReview,show:nextKind}).toString();
    history.replaceState(history.state,'',address);
    const asked=++version.current;
    picks.clear();setMessage('');setError('');
    try{
      const response=await fetch(query(nextReview,nextKind,0));
      if(!response.ok)throw new Error('The screenshots for these filters could not be read. Try again.');
      const fresh:ScreenshotPage=await response.json();
      if(asked!==version.current)return;
      setCurrent(fresh);
      setListed({review:nextReview,kind:nextKind});
      window.scrollTo({top:0});
    }catch(reason){if(asked===version.current)setError((reason as Error).message)}
  }
  function toggle(id:FilterID){
    const next=new Set(on);
    if(next.has(id))next.delete(id);else next.add(id);
    void choose(next);
  }
  usePageFilters(current.unreviewed+current.reviewed>0||on.size>0?{
    options:FILTERS.map(({count,...option})=>({...option,on:on.has(option.id),count:count(current)})),
    toggle:id=>toggle(id as FilterID),clear:()=>void choose(new Set()),
  }:null);

  /** Takes what left the list off it, and out of its counts. */
  function drop(ids:Set<number>,counts:{reviewed?:number;unreviewed?:number}){
    version.current++;
    setCurrent(value=>{
      const gone=value.items.filter(item=>ids.has(item.id));
      const videos=gone.filter(item=>item.kind==='video').length;
      return {...value,items:value.items.filter(item=>!ids.has(item.id)),total:value.total-gone.length,bytes:value.bytes-gone.reduce((sum,item)=>sum+item.size,0),
        reviewed:value.reviewed+(counts.reviewed??0),unreviewed:value.unreviewed+(counts.unreviewed??0),
        stills:value.stills-(gone.length-videos),recordings:value.recordings-videos};
    });
  }

  /** Keep, or put back as not reviewed: a catalogue decision, nothing moves. */
  async function decide(chosen:ScreenshotItem[],status:Status){
    if(chosen.length===0||busy)return;
    setBusy(true);setError('');setMessage('');
    try{
      const saved=await sendDecisions(chosen.map(item=>({assetId:item.id,status,favourite:item.favourite,expectedRevision:item.revision,requestId:requestID()})));
      const moved=chosen.length,label=plural(moved,'screenshot');
      const counts=status==='keep'?{reviewed:moved,unreviewed:-moved}:{reviewed:-moved,unreviewed:moved};
      if(review==='all'){
        // Showing both sides, a decision changes the tile, not the list.
        const revisions=new Map(chosen.map((item,index)=>[item.id,saved[index].revision]));
        setCurrent(value=>({...value,items:value.items.map(item=>revisions.has(item.id)?{...item,status,revision:revisions.get(item.id)!}:item),
          reviewed:value.reviewed+counts.reviewed,unreviewed:value.unreviewed+counts.unreviewed}));
      }else drop(new Set(chosen.map(item=>item.id)),counts);
      picks.clear();
      steps.record(undoableDecisions(status==='keep'?`kept ${label}`:`put ${label} back under Not reviewed`,chosen,chosen.map(item=>({...item,status})),saved));
      setMessage(status==='keep'
        ?`Kept ${label}. ${review==='all'?'Nothing':`${moved===1?'It is':'They are'} under Reviewed now; nothing`} moved on disk.`
        :`Put ${label} back under Not reviewed.`);
    }catch(reason){setError((reason as Error).message)}finally{setBusy(false)}
  }

  /** Remove sends files to the recoverable Bin; archive copies a kept file under
   * the date in its name. Either is queued as a task and leaves the page at
   * once: the server plans and carries out each file in the background, and
   * any it could not handle come back when the task finishes. */
  async function run(chosen:ScreenshotItem[],action:'keep'|'remove'){
    if(chosen.length===0||busy)return;
    setBusy(true);setError('');setMessage('');
    try{
      const ids=chosen.map(item=>item.id);
      const task=await queueScreenshots(ids,action);
      if(action==='remove')flyToBin(ids.map(id=>document.querySelector(`main figure[data-asset="${id}"]`)));
      const gone=new Set(ids),kept=chosen.filter(item=>item.status==='keep').length;
      drop(gone,{reviewed:-kept,unreviewed:-(gone.size-kept)});
      picks.setPicked(previous=>new Set([...previous].filter(id=>!gone.has(id))));
      const label=plural(chosen.length,'screenshot');
      if(action==='remove'){
        steps.record(undoableRemove(`moved ${label} to the Bin`,task,ids));
        setMessage(chosen.length===1?'Moved to the recoverable Bin.':`Moving ${label} to the recoverable Bin. It carries on in the background under Tasks.`);
      }else setMessage(chosen.length===1?`Copying ${chosen[0].name} into the archive, under the date in its name.`:`Copying ${label} into the archive, each under the date in its name. It carries on in the background under Tasks.`);
    }catch(reason){setError((reason as Error).message)}finally{setBusy(false)}
  }

  // A move that could not handle every file leaves those on the page: the
  // list is read again to show them, and says so.
  useEffect(()=>{
    function finished(event:Event){
      const task=(event as CustomEvent<Task>).detail;
      if(!task.kind.startsWith('screenshots.')||task.failed+task.cancelled===0)return;
      void choose(on).then(()=>{
        if(task.failed>0)setError(`${plural(task.failed,'screenshot')} could not be ${task.kind==='screenshots.keep'?'copied into the archive':'moved to the Bin'} and ${task.failed===1?'is':'are'} back on the page. Tasks says why.`);
      });
    }
    window.addEventListener(TASK_FINISHED,finished);
    return()=>window.removeEventListener(TASK_FINISHED,finished);
  });

  // The viewer speaks in photo decisions: Keep keeps, Remove goes to the Bin,
  // Favourite is saved as it is anywhere else. It never advances by itself,
  // because the file just handled leaves the list and the next slides in.
  function decideInViewer(asset:Asset,status:Status,favourite?:boolean){
    const item=items.find(candidate=>candidate.id===asset.id);
    if(!item||busy)return false;
    if(favourite!==undefined){
      void sendDecisions([{assetId:item.id,status:item.status,favourite,expectedRevision:item.revision,requestId:requestID()}])
        .then(([saved])=>setCurrent(value=>({...value,items:value.items.map(entry=>entry.id===item.id?{...entry,favourite,revision:saved.revision}:entry)})))
        .catch(reason=>setError((reason as Error).message));
    }
    else if(status==='cull')void run([item],'remove');
    else if(status==='keep'&&item.status!=='keep')void decide([item],'keep');
    return false;
  }

  // The actions follow what is selected: Keep for what is not reviewed yet,
  // and for what was kept, taking it back or filing it in the archive.
  const chosen=items.filter(item=>picks.picked.has(item.id));
  const allShown=items.length>0&&items.every(item=>picks.picked.has(item.id));
  const waiting=chosen.filter(item=>item.status!=='keep'),kept=chosen.filter(item=>item.status==='keep');
  const dated=kept.filter(item=>item.day);
  const actions:SelectionAction[]=[
    {label:allShown?'Deselect all':`Select all ${items.length} shown`,icon:'select_all',keys:'Mod+A',onClick:()=>allShown?picks.clear():picks.all()},
    ...(waiting.length>0?[{label:waiting.length===chosen.length?'Keep':`Keep the ${waiting.length} not reviewed`,icon:'check',keys:'K',onClick:()=>void decide(waiting,'keep')}] as SelectionAction[]:[]),
    ...(kept.length>0?[
      {label:kept.length===chosen.length?'Mark not reviewed':`Mark the ${kept.length} kept not reviewed`,icon:'undo',keys:'U',onClick:()=>void decide(kept,'unreviewed')},
      {label:dated.length===chosen.length?'Copy into the archive':`Copy the ${dated.length} kept and dated into the archive`,icon:'drive_file_move',keys:'C',disabled:dated.length===0,onClick:()=>void run(dated,'keep')},
    ] as SelectionAction[]:[]),
    {label:'Move to Bin',icon:'delete',keys:'X',danger:true,onClick:()=>void run(chosen,'remove')},
  ];
  useSelectionBar({count:picks.picked.size,busy,clear:picks.clear,actions});
  const viewing=photo.open===null?null:Number(photo.open);

  return <>
    <section className="dupehead">
      <h1>Screenshots</h1>
      <p className="ysum"><b>{total.toLocaleString()}</b> {review==='reviewed'?'reviewed':review===''?'to review':total===1?'screenshot':'screenshots'} · <b>{bytes(totalBytes)}</b></p>
      <p className="hint">Nothing here is in the archive yet. Click a screenshot to look at it; <b>k</b> keeps it, <b>x</b> moves it to the Bin and <b>{undoKeys.undo}</b> takes back each step in turn. Tick the circle on a tile to select it, then click others to add them or Shift-click to add every one between. The actions appear at the top.
        <strong> Keep</strong> moves it to Reviewed without touching the file; from there <strong>Copy into the archive</strong> files it under the date in its name. <strong>Move to Bin</strong> stays recoverable.</p>
    </section>
    {message?<p className="flash" role="status">{message} {steps.latest&&<button className="btn small" disabled={busy||steps.working} {...tipProps('Undo','Mod+Z')} onClick={steps.undo}>Undo</button>}</p>
      :steps.notice&&<p className="flash" role="status">{steps.notice} {steps.canRedo&&<button className="btn small" disabled={busy||steps.working} {...tipProps('Redo','Mod+Shift+Z Mod+U')} onClick={steps.redo}>Redo</button>}</p>}
    {error&&<p className="note warn" role="alert">{error}</p>}
    {items.length===0
      ? <p className="note">{filter?'Nothing here matches these filters.':current.unreviewed+current.reviewed===0?'The holding area is empty.':review==='reviewed'?'Nothing kept yet. Keep a screenshot and it shows here.':'Every screenshot here has been reviewed.'}</p>
      : <Rows className="shots">{items.map((item,index)=>{
          const picked=picks.picked.has(item.id);
          const detail=[item.day||'undated',bytes(item.size)].join(' · ');
          return <RowTile className={picked?'picked':''} key={item.id} data-asset={item.id} ratio={shapeOf(item)} title={`${item.name} · ${detail}`}
            onClick={event=>{if(!picks.tap(index,event.shiftKey))photo.show(item.id)}}>
            <Media asset={item}/>
            <Pick checked={picked} label={`Select ${item.name}, ${detail}`} onToggle={extend=>picks.toggle(index,extend)}/>
            {item.kind==='video'&&<span className="dur"><Icon name="play_circle"/></span>}
            {!item.day&&<div className="bdg"><span className="b warn">undated</span></div>}
          </RowTile>;
        })}</Rows>}
    {viewing!==null&&items.length>0&&<Viewer assets={items} initialID={viewing} onClose={photo.close} onMove={photo.moved} onSave={decideInViewer} onPatch={()=>{}}/>}
    <MoreMarker state={more} what="screenshots"/>
  </>;
}
