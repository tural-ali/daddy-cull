import {binChanged,type Asset} from '../api';
import {useState} from 'react';

type SocialItem=Asset&{
  day:string;name:string;score:number;band:'likely'|'possible'|'watch';
  evidence:string;width:number;height:number;duration:number;letterbox:boolean;poster:boolean;
};
export type SocialPage={items:SocialItem[];total:number;shown:number;bytes:number;likely:number;possible:number;watch:number;letterboxed:number;kept:number;marked:number};

function bytes(value:number){return value<1024**2?`${(value/1024).toFixed(1)} KB`:value<1024**3?`${(value/1024**2).toFixed(1)} MB`:`${(value/1024**3).toFixed(1)} GB`}
function clock(seconds:number){if(!seconds)return '';const s=Math.round(seconds);return s<60?`${s}s`:`${Math.floor(s/60)}m ${String(s%60).padStart(2,'0')}s`}
function requestID(){return Array.from(crypto.getRandomValues(new Uint8Array(16)),byte=>byte.toString(16).padStart(2,'0')).join('')}

const BANDS:{key:string;label:string;count:(page:SocialPage)=>number}[]=[
  {key:'',label:'Everything',count:page=>page.total},
  {key:'likely',label:'Strong evidence',count:page=>page.likely},
  {key:'possible',label:'Some evidence',count:page=>page.possible},
  {key:'watch',label:'Weak signal',count:page=>page.watch},
  {key:'letterboxed',label:'Story letterbox',count:page=>page.letterboxed},
];
const BAND_WORD:Record<SocialItem['band'],string>={likely:'strong',possible:'some',watch:'weak'};

// The server takes twenty decisions per request, so a page of selections goes as
// several batches in order. A failed batch stops the rest and the message says how
// many were already saved, because a half-applied selection the reviewer cannot see
// is worse than one they can.
const PER_REQUEST=20;
function chunk<T>(values:T[],size:number){const out:T[][]=[];for(let i=0;i<values.length;i+=size)out.push(values.slice(i,i+size));return out}

type Change={assetId:number;status:string;favourite:boolean;expectedRevision:number;requestId:string};

async function sendDecisions(changes:Change[]):Promise<{revision:number}[]>{
  const saved:{revision:number}[]=[];
  for(const batch of chunk(changes,PER_REQUEST)){
    const response=await fetch('/api/decisions/batch',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(batch)});
    if(!response.ok)throw new Error(response.status===409
      ?`One of these files changed in another session. ${saved.length} saved; reload before deciding again.`
      :`The decision could not be confirmed. ${saved.length} saved; reload to check before continuing.`);
    saved.push(...await response.json() as {revision:number}[]);
  }
  binChanged();
  return saved;
}

export function Social({page,band,from}:{page:SocialPage;band:string;from:number}){
  const [current,setCurrent]=useState(page);
  const [selected,setSelected]=useState<Set<number>>(new Set());
  const [anchor,setAnchor]=useState<number|null>(null);
  const [playing,setPlaying]=useState<number|null>(null);
  const [busy,setBusy]=useState(false);
  const [message,setMessage]=useState('');
  const [error,setError]=useState('');
  const [undo,setUndo]=useState<{changes:Change[];label:string}|null>(null);
  const {items,total,shown}=current;
  const per=120,to=Math.min(shown,from+items.length);
  const href=(value:string,offset:number)=>`/social?band=${value}&from=${offset}`;

  function toggle(index:number,extend:boolean){
    const item=items[index];
    setSelected(previous=>{
      const next=new Set(previous);
      if(extend&&anchor!==null){
        const start=Math.min(anchor,index),end=Math.max(anchor,index),adding=!next.has(item.id);
        for(let i=start;i<=end;i++){if(adding)next.add(items[i].id);else next.delete(items[i].id)}
        return next;
      }
      if(next.has(item.id))next.delete(item.id);else next.add(item.id);
      return next;
    });
    setAnchor(index);
  }

  async function apply(status:'keep'|'cull'){
    const chosen=items.filter(item=>selected.has(item.id));
    if(chosen.length===0||busy)return;
    setBusy(true);setError('');setMessage('');setUndo(null);
    const changes:Change[]=chosen.map(item=>({assetId:item.id,status,favourite:item.favourite,expectedRevision:item.revision,requestId:requestID()}));
    try{
      const saved=await sendDecisions(changes);
      const gone=new Set(chosen.map(item=>item.id));
      const freed=chosen.reduce((sum,item)=>sum+item.size,0);
      const count=`${chosen.length} ${chosen.length===1?'video':'videos'}`;
      setCurrent(value=>({...value,
        items:value.items.filter(item=>!gone.has(item.id)),
        total:value.total-chosen.length,
        shown:value.shown-chosen.length,
        bytes:value.bytes-freed,
        likely:value.likely-chosen.filter(item=>item.band==='likely').length,
        possible:value.possible-chosen.filter(item=>item.band==='possible').length,
        watch:value.watch-chosen.filter(item=>item.band==='watch').length,
        letterboxed:value.letterboxed-chosen.filter(item=>item.letterbox).length,
        kept:value.kept+(status==='keep'?chosen.length:0),
        marked:value.marked+(status==='cull'?chosen.length:0),
      }));
      setSelected(new Set());setAnchor(null);
      // What was decided has left the list, so reading the same offset again
      // brings the next videos up into view instead of leaving the page empty.
      // Past the last page, the one before it is shown instead.
      try{
        const response=await fetch(`/api/social?band=${encodeURIComponent(band)}&from=${from}`);
        if(response.ok){
          const next:SocialPage=await response.json();
          if(next.items.length===0&&from>0){location.assign(href(band,Math.max(0,from-per)));return}
          setCurrent(next);
        }
      }catch{/* the local list above is already correct, only shorter */}
      setUndo({
        changes:chosen.map((item,index)=>({assetId:item.id,status:'unreviewed',favourite:item.favourite,expectedRevision:saved[index].revision,requestId:requestID()})),
        label:count,
      });
      setMessage(status==='keep'
        ?`Kept ${count}. They have left this list and not moved on disk.`
        :`Marked ${count} for the Bin, ${bytes(freed)} in all. Nothing has moved: open Bin to carry it out, and it stays recoverable after that.`);
    }catch(reason){setError((reason as Error).message)}finally{setBusy(false)}
  }

  async function revert(){
    if(!undo||busy)return;
    setBusy(true);setError('');
    try{await sendDecisions(undo.changes);location.reload()}
    catch(reason){setError((reason as Error).message);setBusy(false)}
  }

  const allShown=items.length>0&&items.every(item=>selected.has(item.id));
  return <>
    <section className="dupehead">
      <h1>Saved from social</h1>
      <p className="ysum"><b>{total.toLocaleString()}</b> undecided · <b>{bytes(current.bytes)}</b>
        {shown>0&&<span className="dim"> · showing {(from+1).toLocaleString()}–{to.toLocaleString()} of {shown.toLocaleString()}</span>}
        {(current.kept>0||current.marked>0)&&<span className="dim"> · {current.kept.toLocaleString()} kept, {current.marked.toLocaleString()} marked for the Bin</span>}
      </p>
      <p className="pager">{BANDS.map(entry=><a className={`btn small${band===entry.key?' on':''}`} aria-current={band===entry.key||undefined} href={href(entry.key,0)} key={entry.label}>{entry.label} <span className="dim">{entry.count(current).toLocaleString()}</span></a>)}</p>
      <p className="note">
        Read from container headers and six greyscale thumbnails per video. No pixels were sent anywhere and no model was used.
        The chip on each still is the evidence score: how strongly the file's own metadata says it came out of an app rather than a lens.
        A high score means no camera fingerprint and a download-shaped name. Which app, the headers cannot say.
        In a 60-file sample of <strong>strong evidence</strong>, about half also showed Story furniture in the still and the rest were plain clips with no capture metadata.
        <strong> Story letterbox</strong> is the surest visual tell and ran about nine in ten true on inspection.
      </p>
      <p className="note">
        Select with the tick, or click a still; shift-click extends the run.
        <strong> Keep</strong> takes a video off this list and changes nothing on disk.
        <strong> Move to Bin</strong> marks it for removal: the file moves only when you run the Bin, and is recoverable after that.
      </p>
    </section>
    {message&&<p className="flash" role="status">{message} {undo&&<button className="btn small" disabled={busy} onClick={()=>void revert()}>Undo {undo.label}</button>}</p>}
    {error&&<p className="note warn" role="alert">{error}</p>}
    {items.length>0&&<div className="selbar" role="group" aria-label="Selected videos">
      <label className="selall"><input type="checkbox" checked={allShown} onChange={()=>{setSelected(allShown?new Set():new Set(items.map(item=>item.id)));setAnchor(null)}}/> Select all {items.length} shown</label>
      <span className="dim">{selected.size.toLocaleString()} selected</span>
      <button type="button" className="btn small" disabled={busy||selected.size===0} onClick={()=>void apply('keep')}>{busy?'Saving…':'Keep'}</button>
      <button type="button" className="btn small danger ghosty" disabled={busy||selected.size===0} onClick={()=>void apply('cull')}>Move to Bin</button>
      <button type="button" className="btn small" data-sel="clear" disabled={busy||selected.size===0} onClick={()=>{setSelected(new Set());setAnchor(null)}}>Clear</button>
    </div>}
    {items.length===0
      ? <p className="note">{total===0?'Every candidate has been decided. Nothing is left to review.':'Nothing matches this filter.'}</p>
      : <div className="socialgrid">{items.map((item,index)=><figure className={`socialcard${selected.has(item.id)?' picked':''}`} key={item.id}>
          <div className="socialshot" onClick={event=>toggle(index,event.shiftKey)}>
            {playing===item.id
              // Judging a clip from one frame is guesswork, so the still swaps for
              // the video in place. Clicks inside the player must not reach the
              // card, or scrubbing would toggle the selection underneath it.
              ? <video className="socialplayer" controls autoPlay playsInline preload="metadata"
                  poster={item.poster?`/api/social-poster/${item.id}`:undefined}
                  src={`/api/media/${item.id}/original`}
                  onClick={event=>event.stopPropagation()}
                  onEnded={()=>setPlaying(null)}/>
              : item.poster
                ? <img src={`/api/social-poster/${item.id}`} alt={item.name} loading="lazy" decoding="async"/>
                : <div className="media-missing"><span>No still captured</span><small>{item.kind.toUpperCase()} · Original untouched</small></div>}
            <div className="bdg">
              <span className={`b score ${item.band}`} title={`Evidence score ${item.score}: ${BAND_WORD[item.band]} evidence this came from an app rather than a camera`}>{BAND_WORD[item.band]} {item.score}</span>
              {item.letterbox&&<span className="b play">letterbox</span>}
            </div>
            <button type="button" className="socialplay" aria-label={playing===item.id?`Stop ${item.name}`:`Play ${item.name}`}
              onClick={event=>{event.stopPropagation();setPlaying(current=>current===item.id?null:item.id)}}>{playing===item.id?'■':'▶'}</button>
            <label className="socialpick" onClick={event=>event.stopPropagation()}>
              <input type="checkbox" checked={selected.has(item.id)} onChange={event=>toggle(index,(event.nativeEvent as MouseEvent).shiftKey)} aria-label={`Select ${item.name}`}/>
            </label>
          </div>
          <figcaption>
            <span className="socialday"><span>{item.day||'undated'}</span><span className="dim socialspec">{item.width>0&&`${item.width}×${item.height}`}{item.duration>0&&` · ${clock(item.duration)}`}</span></span>
            <span className="dim socialspec">{bytes(item.size)}</span>
            <span className="socialwhy" title={item.evidence}>{item.evidence}</span>
            <span className="socialname dim" title={item.path}>{item.name}</span>
          </figcaption>
        </figure>)}</div>}
    {items.length>0&&<p className="pager">{from>0&&<a className="btn small" href={href(band,Math.max(0,from-per))}>← Previous</a>}{to<shown&&<a className="btn small" href={href(band,from+per)}>Next {per} →</a>}</p>}
  </>;
}
