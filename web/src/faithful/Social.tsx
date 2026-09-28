import {type Asset,type Status} from '../api';
import {useRef,useState} from 'react';
import {Icon} from '../Icon';
import {Pick,RowTile,Rows,useSelectionBar,usePicks} from './selection';
import {Viewer} from './Viewer';
import {usePhotoURL} from './photoURL';
import {usePageFilters} from './SearchFilters';
import {MoreMarker,useMoreOnScroll} from './more';
import {reverting,requestID,sendDecisions,type Change} from './decisions';
import {reloadPage} from './router';
import {flyToBin} from './binFlight';

type SocialItem=Asset&{
  day:string;name:string;score:number;band:'likely'|'possible'|'watch';
  evidence:string;width:number;height:number;duration:number;letterbox:boolean;poster:boolean;
};
export type SocialPage={items:SocialItem[];total:number;shown:number;bytes:number;likely:number;possible:number;watch:number;letterboxed:number;social:number;unsure:number;kept:number;marked:number};

function bytes(value:number){return value<1024**2?`${(value/1024).toFixed(1)} KB`:value<1024**3?`${(value/1024**2).toFixed(1)} MB`:`${(value/1024**3).toFixed(1)} GB`}
function clock(seconds:number){if(!seconds)return '';const s=Math.round(seconds);return s<60?`${s}s`:`${Math.floor(s/60)}m ${String(s%60).padStart(2,'0')}s`}
// The two answers are filters in the search bar; both, like neither, show
// every candidate.
type Band='social'|'unsure';
const BANDS:{id:Band;label:string;icon:'forum'|'info';words:string[];count:(page:SocialPage)=>number}[]=[
  {id:'social',label:'Likely social',icon:'forum',words:['likely social','social','saved from an app'],count:page=>page.social},
  {id:'unsure',label:'Not sure',icon:'info',words:['not sure','unsure','maybe'],count:page=>page.unsure},
];
function bandFor(on:ReadonlySet<Band>){return on.size===1?[...on][0]:''}
function query(band:string,from:number){return `/api/social?band=${encodeURIComponent(band)}&from=${from}`}

// Each tile gives one answer. Strong evidence, or a Story letterbox (about
// nine in ten true on inspection), is likely social; anything less is a
// maybe that deserves a look before it goes.
function likelySocial(item:SocialItem){return item.band==='likely'||item.letterbox}

export function Social({page,band:initialBand}:{page:SocialPage;band:string}){
  const [current,setCurrent]=useState(page);
  const [on,setOn]=useState<ReadonlySet<Band>>(()=>new Set(initialBand==='social'||initialBand==='unsure'?[initialBand]:[]));
  // The search bar shows the filters as switched; the list keeps its own
  // until the list for the new ones lands.
  const [band,setBand]=useState(()=>bandFor(on));
  // Bumped whenever the list changes under a read in flight, which then
  // lands nowhere: its offset no longer points where it did.
  const version=useRef(0);
  const [busy,setBusy]=useState(false);
  const [message,setMessage]=useState('');
  const [error,setError]=useState('');
  const [undo,setUndo]=useState<{changes:Change[];label:string}|null>(null);
  const {items,total,shown}=current;
  const picks=usePicks(items,item=>item.id);
  const photo=usePhotoURL(id=>page.items.some(item=>String(item.id)===id));
  const selected=picks.picked;

  // Read at the length loaded: what was decided has already left the list
  // here and on the server, so the offsets agree.
  const more=useMoreOnScroll(band,items.length<shown,items.length,async()=>{
    const asked=version.current;
    const response=await fetch(query(band,items.length));
    if(!response.ok)throw new Error('more');
    const next:SocialPage=await response.json();
    if(asked!==version.current)return;
    setCurrent(value=>{
      const have=new Set(value.items.map(item=>item.id));
      return {...next,items:[...value.items,...next.items.filter(item=>!have.has(item.id))]};
    });
  });
  async function choose(next:ReadonlySet<Band>){
    setOn(next);
    const address=new URL(location.href);
    address.search=new URLSearchParams({band:bandFor(next)}).toString();
    history.replaceState(history.state,'',address);
    const asked=++version.current;
    picks.clear();setMessage('');setUndo(null);setError('');
    try{
      const response=await fetch(query(bandFor(next),0));
      if(!response.ok)throw new Error('The videos for these filters could not be read. Try again.');
      const fresh:SocialPage=await response.json();
      if(asked!==version.current)return;
      setCurrent(fresh);
      setBand(bandFor(next));
      window.scrollTo({top:0});
    }catch(reason){if(asked===version.current)setError((reason as Error).message)}
  }
  usePageFilters(total>0||on.size>0?{
    options:BANDS.map(({count,...option})=>({...option,group:'Answer',on:on.has(option.id),count:count(current)})),
    toggle:id=>{const next=new Set(on);if(next.has(id as Band))next.delete(id as Band);else next.add(id as Band);void choose(next)},
    clear:()=>void choose(new Set()),
  }:null);

  async function apply(status:'keep'|'cull',chosen=items.filter(item=>selected.has(item.id))){
    if(chosen.length===0||busy)return;
    setBusy(true);setError('');setMessage('');setUndo(null);
    const changes:Change[]=chosen.map(item=>({assetId:item.id,status,favourite:status!=='cull'&&item.favourite,expectedRevision:item.revision,requestId:requestID()}));
    try{
      const saved=await sendDecisions(changes);
      if(status==='cull')flyToBin(chosen.map(item=>document.querySelector(`main figure[data-asset="${item.id}"]`)));
      const gone=new Set(chosen.map(item=>item.id));
      const freed=chosen.reduce((sum,item)=>sum+item.size,0);
      const count=`${chosen.length} ${chosen.length===1?'video':'videos'}`;
      version.current++;
      setCurrent(value=>({...value,
        items:value.items.filter(item=>!gone.has(item.id)),
        total:value.total-chosen.length,
        shown:value.shown-chosen.length,
        bytes:value.bytes-freed,
        likely:value.likely-chosen.filter(item=>item.band==='likely').length,
        social:value.social-chosen.filter(likelySocial).length,
        unsure:value.unsure-chosen.filter(item=>!likelySocial(item)).length,
        possible:value.possible-chosen.filter(item=>item.band==='possible').length,
        watch:value.watch-chosen.filter(item=>item.band==='watch').length,
        letterboxed:value.letterboxed-chosen.filter(item=>item.letterbox).length,
        kept:value.kept+(status==='keep'?chosen.length:0),
        marked:value.marked+(status==='cull'?chosen.length:0),
      }));
      picks.setPicked(previous=>new Set([...previous].filter(id=>!gone.has(id))));
      setUndo({changes:reverting(chosen,saved),label:count});
      setMessage(status==='keep'
        ?`Kept ${count}. They have left this list and not moved on disk.`
        :`Marked ${count} for the Bin, ${bytes(freed)} in all. Nothing has moved: open Bin to carry it out, and it stays recoverable after that.`);
    }catch(reason){setError((reason as Error).message)}finally{setBusy(false)}
  }

  async function revert(){
    if(!undo||busy)return;
    setBusy(true);setError('');
    try{await sendDecisions(undo.changes);reloadPage()}
    catch(reason){setError((reason as Error).message);setBusy(false)}
  }

  // The viewer's Keep and Remove act on the one video showing, as the
  // selection bar does on many. It never advances by itself: the video just
  // decided leaves the list and the next one slides into its place.
  function decideInViewer(asset:Asset,status:Status,favourite?:boolean){
    const item=items.find(candidate=>candidate.id===asset.id);
    if(!item||busy)return false;
    if(favourite!==undefined){
      void sendDecisions([{assetId:item.id,status:item.status,favourite,expectedRevision:item.revision,requestId:requestID()}])
        .then(([saved])=>setCurrent(value=>({...value,items:value.items.map(entry=>entry.id===item.id?{...entry,favourite,revision:saved.revision}:entry)})))
        .catch(reason=>setError((reason as Error).message));
    }
    else if(status==='keep'||status==='cull')void apply(status,[item]);
    return false;
  }

  const allShown=items.length>0&&items.every(item=>selected.has(item.id));
  const viewing=photo.open===null?null:Number(photo.open);
  useSelectionBar({count:selected.size,busy,clear:picks.clear,actions:[
    {label:allShown?'Deselect all':`Select all ${items.length} shown`,icon:'select_all',keys:'Mod+A',onClick:()=>allShown?picks.clear():picks.all()},
    {label:'Keep',icon:'check',keys:'K',onClick:()=>void apply('keep')},
    {label:'Move to Bin',icon:'delete',keys:'X',onClick:()=>void apply('cull'),danger:true},
  ]});
  return <>
    <section className="dupehead">
      <h1>Saved from social</h1>
      <p className="ysum"><b>{total.toLocaleString()}</b> undecided · <b>{bytes(current.bytes)}</b>
        {band&&<span className="dim"> · {shown.toLocaleString()} {band==='social'?'likely social':'not sure'}</span>}
        {(current.kept>0||current.marked>0)&&<span className="dim"> · {current.kept.toLocaleString()} kept, {current.marked.toLocaleString()} marked for the Bin</span>}
      </p>
      <p className="hint">Videos that look saved from an app rather than filmed on a camera, judged from each file's own metadata. <strong>Likely social</strong> means strong evidence; <strong>Not sure</strong> is worth a look before it goes.
        Click a video to watch it; <b>k</b> keeps it and <b>x</b> moves it to the Bin. Tick the circle on a tile to select several, and the actions appear at the top. <strong>Keep</strong> takes a video off this list; <strong>Move to Bin</strong> marks it for the Bin, where it stays recoverable.</p>
    </section>
    {message&&<p className="flash" role="status">{message} {undo&&<button className="btn small" disabled={busy} onClick={()=>void revert()}>Undo {undo.label}</button>}</p>}
    {error&&<p className="note warn" role="alert">{error}</p>}
    {items.length===0
      ? <p className="note">{total===0?'Every candidate has been decided. Nothing is left to review.':'Nothing matches this filter.'}</p>
      : <Rows className="socials">{items.map((item,index)=>{
          const social=likelySocial(item);
          const detail=[item.day||'undated',bytes(item.size),item.duration>0?clock(item.duration):''].filter(Boolean).join(' · ');
          const picked=selected.has(item.id);
          return <RowTile className={`social${picked?' picked':''}`} key={item.id} data-asset={item.id} ratio={item.width>0&&item.height>0?item.width/item.height:undefined} title={detail}
            onClick={event=>{if(!picks.tap(index,event.shiftKey))photo.show(item.id)}}>
            {item.poster
              ? <img src={`/api/social-poster/${item.id}`} alt={item.name} loading="lazy" decoding="async"/>
              : <div className="media-missing"><span>No still captured</span></div>}
            <Pick checked={picked} label={`Select ${item.name}, ${social?'likely social':'not sure'}, ${detail}`} onToggle={extend=>picks.toggle(index,extend)}/>
            {item.duration>0&&<span className="dur">{clock(item.duration)}<Icon name="play_circle"/></span>}
            <div className="bdg"><span className={`b ${social?'social':'unsure'}`}>{social?'Likely social':'Not sure'}</span></div>
          </RowTile>;
        })}</Rows>}
    {viewing!==null&&items.length>0&&<Viewer assets={items} initialID={viewing} onClose={photo.close} onMove={photo.moved} onSave={decideInViewer} onPatch={()=>{}}/>}
    <MoreMarker state={more} what="videos"/>
  </>;
}
