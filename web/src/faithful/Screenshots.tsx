import {Media} from '../Media';
import {binChanged,type Asset,type Status} from '../api';
import {Icon} from '../Icon';
import {Viewer} from './Viewer';
import {useState} from 'react';
import {Pick,RowTile,Rows,useSelectionBar,usePicks,type SelectionAction} from './selection';
import {usePhotoURL} from './photoURL';
import {reverting,requestID,sendDecisions,type Change} from './decisions';

type ScreenshotItem=Asset&{day:string;name:string;state:string};
export type ScreenshotPage={items:ScreenshotItem[];total:number;bytes:number;unreviewed:number;reviewed:number};
export type ScreenshotPlan={id:string;assetId:number;action:'keep'|'remove';state:string;created:string;files:{source:string;destination:string;size:number;hash:string;sidecar:boolean;phase:string}[];error?:string};
function bytes(value:number){return value<1024**2?`${(value/1024).toFixed(1)} KB`:value<1024**3?`${(value/1024**2).toFixed(1)} MB`:`${(value/1024**3).toFixed(1)} GB`}
function plural(count:number,one:string){return `${count.toLocaleString()} ${one}${count===1?'':'s'}`}
async function post<T>(path:string,body:unknown):Promise<T>{
  const response=await fetch(path,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});
  const text=await response.text();
  let result:unknown;
  try{result=JSON.parse(text)}catch{throw new Error('The screenshot result could not be confirmed. Reload before retrying.')}
  if(!response.ok)throw new Error((result as {error?:string}).error||'The screenshot action failed safely.');
  binChanged();
  return result as T;
}

// Two filters, as in Lightroom: what has not been looked at yet, and what has
// been kept. Keeping is a decision only; the file stays where it is.
const REVIEWS:{key:string;label:string;count:(page:ScreenshotPage)=>number}[]=[
  {key:'',label:'Not reviewed',count:page=>page.unreviewed},
  {key:'reviewed',label:'Reviewed',count:page=>page.reviewed},
];
const KINDS:[string,string][]=[['','Everything'],['image','Stills'],['video','Recordings']];

type Undo={label:string;changes?:Change[];plans?:ScreenshotPlan[]};

export function Screenshots({page,filter,review,from}:{page:ScreenshotPage;filter:string;review:string;from:number}){
  const [current,setCurrent]=useState(page);
  const [busy,setBusy]=useState(false);
  const [message,setMessage]=useState('');
  const [error,setError]=useState('');
  const [undo,setUndo]=useState<Undo|null>(null);
  const {items,total,bytes:totalBytes}=current;
  const picks=usePicks(items,item=>item.id);
  const photo=usePhotoURL(id=>page.items.some(item=>String(item.id)===id));
  const per=120,to=Math.min(total,from+items.length);
  const href=(nextReview:string,kind:string,offset:number)=>`/screenshots?review=${nextReview}&show=${kind}&from=${offset}`;

  // What was decided has left this filter, so reading the same offset again
  // brings the next screenshots up instead of leaving a hole; past the last
  // page, the one before it is shown.
  async function refill(){
    try{
      const response=await fetch(`/api/screenshots?kind=${encodeURIComponent(filter)}&review=${review}&from=${from}`);
      if(!response.ok)return;
      const next:ScreenshotPage=await response.json();
      if(next.items.length===0&&from>0){location.assign(href(review,filter,Math.max(0,from-per)));return}
      setCurrent(next);
    }catch{/* the list already dropped what was decided, it is only shorter */}
  }
  function drop(ids:Set<number>,counts:{reviewed?:number;unreviewed?:number}){
    setCurrent(value=>{
      const gone=value.items.filter(item=>ids.has(item.id));
      return {...value,items:value.items.filter(item=>!ids.has(item.id)),total:value.total-gone.length,bytes:value.bytes-gone.reduce((sum,item)=>sum+item.size,0),
        reviewed:value.reviewed+(counts.reviewed??0),unreviewed:value.unreviewed+(counts.unreviewed??0)};
    });
  }

  /** Keep, or put back as not reviewed: a catalogue decision, nothing moves. */
  async function decide(chosen:ScreenshotItem[],status:Status){
    if(chosen.length===0||busy)return;
    setBusy(true);setError('');setMessage('');setUndo(null);
    try{
      const saved=await sendDecisions(chosen.map(item=>({assetId:item.id,status,favourite:item.favourite,expectedRevision:item.revision,requestId:requestID()})));
      const moved=chosen.length,label=plural(moved,'screenshot');
      drop(new Set(chosen.map(item=>item.id)),status==='keep'?{reviewed:moved,unreviewed:-moved}:{reviewed:-moved,unreviewed:moved});
      picks.clear();
      setUndo({label,changes:reverting(chosen,saved)});
      setMessage(status==='keep'?`Kept ${label}. ${moved===1?'It is':'They are'} under Reviewed now; nothing moved on disk.`:`Put ${label} back under Not reviewed.`);
      await refill();
    }catch(reason){setError((reason as Error).message)}finally{setBusy(false)}
  }

  /** Remove sends files to the recoverable Bin; archive copies a kept file under
   * the date in its name. Each file is its own verified plan, run one by one,
   * and a failure stops the rest and says how far it got. */
  async function run(chosen:ScreenshotItem[],action:'keep'|'remove'){
    if(chosen.length===0||busy)return;
    setBusy(true);setError('');setMessage('');setUndo(null);
    const done:ScreenshotPlan[]=[];
    try{
      for(const item of chosen){
        const plan=await post<ScreenshotPlan>('/api/screenshot-actions/preview',{assetId:item.id,action});
        done.push(await post<ScreenshotPlan>('/api/screenshot-actions/execute',{id:plan.id}));
      }
    }catch(reason){setError(`${(reason as Error).message} ${done.length} of ${chosen.length} done before it stopped.`)}
    const finished=new Set(done.map(plan=>plan.assetId));
    const kept=chosen.filter(item=>finished.has(item.id)&&item.status==='keep').length;
    drop(finished,{reviewed:-kept,unreviewed:-(finished.size-kept)});
    picks.setPicked(previous=>new Set([...previous].filter(id=>!finished.has(id))));
    if(done.length>0){
      const label=plural(done.length,'screenshot');
      if(action==='remove'){setUndo({label,plans:done});setMessage(`Moved ${label} to the recoverable Bin.`)}
      else setMessage(done.length===1?`Copied ${chosen[0].name} into ${done[0].files[0].destination}.`:`Copied ${label} into the archive, each under the date in its name.`);
      await refill();
    }
    setBusy(false);
  }

  async function revert(){
    if(!undo||busy)return;
    setBusy(true);setError('');
    try{
      if(undo.changes)await sendDecisions(undo.changes);
      for(const plan of undo.plans??[])await post<ScreenshotPlan>('/api/screenshot-actions/undo',{id:plan.id});
      location.reload();
    }catch(reason){setError((reason as Error).message);setBusy(false)}
  }

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

  const chosen=items.filter(item=>picks.picked.has(item.id));
  const allShown=items.length>0&&items.every(item=>picks.picked.has(item.id));
  const dated=chosen.filter(item=>item.day);
  const actions:SelectionAction[]=[
    {label:allShown?'Deselect all':`Select all ${items.length} shown`,icon:'select_all',onClick:()=>allShown?picks.clear():picks.all()},
    ...(review==='reviewed'
      ?[
        {label:'Mark not reviewed',icon:'undo',onClick:()=>void decide(chosen,'unreviewed')},
        {label:dated.length===chosen.length?'Copy into the archive':`Copy the ${dated.length} dated into the archive`,icon:'drive_file_move',disabled:dated.length===0,onClick:()=>void run(dated,'keep')},
      ] as SelectionAction[]
      :[{label:'Keep',icon:'check',onClick:()=>void decide(chosen,'keep')}] as SelectionAction[]),
    {label:'Move to Bin',icon:'delete',danger:true,onClick:()=>void run(chosen,'remove')},
  ];
  useSelectionBar({count:picks.picked.size,busy,clear:picks.clear,actions});
  const viewing=photo.open===null?null:Number(photo.open);

  return <>
    <section className="dupehead">
      <h1>Screenshots</h1>
      <p className="ysum"><b>{total.toLocaleString()}</b> {review==='reviewed'?'reviewed':'to review'} · <b>{bytes(totalBytes)}</b>{total>0&&<span className="dim"> · showing {(from+1).toLocaleString()}–{to.toLocaleString()}</span>}</p>
      <div className="pager filters">
        <span className="fgroup" role="group" aria-label="Review">{REVIEWS.map(entry=><a className={`btn small${review===entry.key?' on':''}`} aria-current={review===entry.key||undefined} href={href(entry.key,filter,0)} key={entry.label}>{entry.label} <span className="dim">{entry.count(current).toLocaleString()}</span></a>)}</span>
        <span className="fgroup" role="group" aria-label="Kind">{KINDS.map(([kind,label])=><a className={`btn small${filter===kind?' on':''}`} aria-current={filter===kind||undefined} href={href(review,kind,0)} key={label}>{label}</a>)}</span>
      </div>
      <p className="hint">Nothing here is in the archive yet. Click a screenshot to look at it; <b>k</b> keeps it and <b>x</b> moves it to the Bin. Tick the circle on a tile to select several, and the actions appear at the top.
        <strong> Keep</strong> moves it to Reviewed without touching the file; from there <strong>Copy into the archive</strong> files it under the date in its name. <strong>Move to Bin</strong> stays recoverable.</p>
    </section>
    {message&&<p className="flash" role="status">{message} {undo&&<button className="btn small" disabled={busy} onClick={()=>void revert()}>Undo</button>}</p>}
    {error&&<p className="note warn" role="alert">{error}</p>}
    {items.length===0
      ? <p className="note">{review==='reviewed'?'Nothing kept yet. Keep a screenshot and it shows here.':current.unreviewed+current.reviewed===0?'The holding area is empty.':'Every screenshot here has been reviewed.'}</p>
      : <Rows className="shots">{items.map((item,index)=>{
          const picked=picks.picked.has(item.id);
          const detail=[item.day||'undated',bytes(item.size)].join(' · ');
          return <RowTile className={picked?'picked':''} key={item.id} title={`${item.name} · ${detail}`}
            onClick={event=>{if(event.shiftKey&&picks.picked.size>0)picks.toggle(index,true);else photo.show(item.id)}}>
            <Media asset={item}/>
            <Pick checked={picked} label={`Select ${item.name}, ${detail}`} onToggle={extend=>picks.toggle(index,extend)}/>
            {item.kind==='video'&&<span className="dur"><Icon name="play_circle"/></span>}
            {!item.day&&<div className="bdg"><span className="b warn">undated</span></div>}
          </RowTile>;
        })}</Rows>}
    {viewing!==null&&items.length>0&&<Viewer assets={items} initialID={viewing} onClose={photo.close} onMove={photo.moved} onSave={decideInViewer} onPatch={()=>{}}/>}
    {items.length>0&&(from>0||to<total)&&<p className="pager">{from>0&&<a className="btn small" href={href(review,filter,Math.max(0,from-per))}>← Previous</a>}{to<total&&<a className="btn small" href={href(review,filter,from+per)}>Next {per} →</a>}</p>}
  </>;
}
