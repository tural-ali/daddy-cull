import {useEffect,useRef,useState,type PointerEvent} from 'react';
import type {Asset} from '../api';
import {Busy} from '../Busy';
import {ProgressBar} from '../ProgressBar';

type Pan={x:number;y:number};
const nameOf=(asset:Asset)=>asset.path.split('/').pop()||'Photo';
const clamp=(value:number,zoom:number)=>Math.max(-(zoom-1)/(2*zoom),Math.min((zoom-1)/(2*zoom),value));

/** Browsing, pinning a reference and choosing a keeper are separate actions. */
export function Compare({assets,focus,onFocus,onClose,onChoose,onRefresh,busy,pending,notice}:{assets:Asset[];focus:number;onFocus:(index:number)=>void;onClose:()=>void;onChoose:(mode:'keep-all'|'keep-keeper',keeper?:number)=>void;onRefresh:()=>void;busy:boolean;pending:boolean;notice:string}){
  const [zoom,setZoom]=useState(1);
  const [pan,setPan]=useState<Pan>({x:0,y:0});
  const [reference,setReference]=useState(0);
  const [keeper,setKeeper]=useState<number|null>(null);
  const firstControl=useRef<HTMLButtonElement>(null);
  const root=useRef<HTMLDivElement>(null);
  const drag=useRef<{x:number;y:number;px:number;py:number;w:number;h:number}|null>(null);
  const other=focus===reference?assets.findIndex((_,index)=>index!==reference):focus;
  const changeZoom=(value:number)=>{setZoom(Math.max(1,Math.min(8,value)));setPan({x:0,y:0})};
  useEffect(()=>{firstControl.current?.focus()},[]);
  useEffect(()=>{
    const element=root.current;
    if(!element)return;
    const trap=(event:KeyboardEvent)=>{
      if(event.key!=='Tab')return;
      const controls=[...element.querySelectorAll<HTMLButtonElement>('button:not(:disabled)')];
      const first=controls[0],last=controls[controls.length-1];
      if(event.shiftKey&&document.activeElement===first){event.preventDefault();last?.focus()}
      else if(!event.shiftKey&&document.activeElement===last){event.preventDefault();first?.focus()}
    };
    element.addEventListener('keydown',trap);return()=>element.removeEventListener('keydown',trap);
  },[]);
  useEffect(()=>{
    const keys=(event:KeyboardEvent)=>{
      if(event.ctrlKey||event.metaKey||event.altKey||busy||pending)return;
      if(event.key==='+'||event.key==='='){event.preventDefault();changeZoom(zoom+1)}
      if(event.key==='-'){event.preventDefault();changeZoom(zoom-1)}
      if(zoom>1&&event.key.startsWith('Arrow')){
        event.preventDefault();
        setPan(value=>({x:clamp(value.x+(event.key==='ArrowLeft'?.05:event.key==='ArrowRight'?-.05:0),zoom),y:clamp(value.y+(event.key==='ArrowUp'?.05:event.key==='ArrowDown'?-.05:0),zoom)}));
      }
    };
    window.addEventListener('keydown',keys);return()=>window.removeEventListener('keydown',keys);
  },[zoom,busy,pending]);
  function start(event:PointerEvent<HTMLElement>){
    if(zoom===1)return;
    const image=event.currentTarget.querySelector('img')?.getBoundingClientRect();
    if(!image)return;
    event.currentTarget.setPointerCapture(event.pointerId);
    drag.current={x:event.clientX,y:event.clientY,px:pan.x,py:pan.y,w:image.width,h:image.height};
  }
  function move(event:PointerEvent<HTMLElement>){
    const from=drag.current;if(!from)return;
    setPan({x:clamp(from.px+(event.clientX-from.x)/from.w,zoom),y:clamp(from.py+(event.clientY-from.y)/from.h,zoom)});
  }
  return <div ref={root} className="rvcmp burstcompare" role="dialog" aria-modal="true" aria-label="Compare nearby photos" tabIndex={-1}>
    <div className="ctop"><b>Compare nearby photos</b><span>{assets.length} {assets.length===1?'photo':'photos'}</span><button className="btn" onClick={()=>changeZoom(zoom-1)} disabled={zoom===1||pending} aria-label="Zoom out">−</button><span>{zoom}×</span><button className="btn" onClick={()=>changeZoom(zoom+1)} disabled={zoom===8||pending} aria-label="Zoom in">+</button><button className="btn" onClick={()=>changeZoom(1)} disabled={pending}>Fit</button><button ref={firstControl} className="rvx" aria-label="Close compare" disabled={busy} onClick={onClose}>×</button></div>
    {pending?<div className="compareempty"><Busy label="Finding nearby shots" size={64}/><p>Checking capture times and visual similarity. You can close this while analysis continues.</p></div>:assets.length<2?<div className="compareempty"><p>No nearby shots found in this folder.</p>{notice&&<p className="hint" role="status">{notice}</p>}<button className="btn" onClick={onRefresh}>Check again</button></div>:<>
      <p className="hint">Linked zoom and drag · + / − zoom · arrow keys pan · 1–9 inspects a photo. Choose a keeper separately.</p>
      {notice&&<p className="hint" role="status">{notice} <button className="textbtn" onClick={onRefresh}>Refresh candidates</button></p>}
      <div className="comparepair">{[reference,other].map((index,side)=>{
        const asset=assets[index];
        return <div className={`compareframe${focus===index?' focused':''}${keeper===index?' keeper':''}`} key={side}>
          <CompareImage key={asset.id} asset={asset} zoom={zoom} pan={pan} onPointerDown={start} onPointerMove={move} onPointerUp={()=>{drag.current=null}}/>
          <div className="comparecaption"><button className="textbtn" aria-pressed={focus===index} onClick={()=>onFocus(index)} title={nameOf(asset)}>{nameOf(asset)}</button><span>{side===0?'Reference':'Candidate'}</span></div>
          <button className="btn keeperchoice" aria-pressed={keeper===index} disabled={busy} onClick={()=>setKeeper(index)}>{keeper===index?'Keeper chosen':'Choose as keeper'}</button>
        </div>;
      })}</div>
      <div className="comparestrip" aria-label="Comparison candidates">{assets.map((asset,index)=><button className={focus===index?'on':''} aria-pressed={focus===index} key={asset.id} onClick={()=>onFocus(index)} title={asset.comparisonReason}><img src={`/api/media/${asset.id}/preview?size=grid`} alt=""/><b>{index+1}. {nameOf(asset)}</b><small>{keeper===index?'Keeper · ':''}{asset.comparisonReason||'Related file'}</small></button>)}</div>
      <div className="comparereference"><button className="textbtn" disabled={busy||focus===reference} onClick={()=>{setReference(focus);changeZoom(1)}}>Use inspected photo as reference</button><span className="hint">RAW and HEIC use previews up to 2560 px.</span></div>
      <div className="cbot"><span className="hint">{keeper===null?'Choose a keeper before removing the other shown photos.':`Keeper: ${nameOf(assets[keeper])}`}</span><button className="rvbtn" disabled={busy} onClick={()=>onChoose('keep-all')}>Keep all</button><button className="rvbtn cull" disabled={busy||keeper===null} onClick={()=>{if(keeper!==null)onChoose('keep-keeper',keeper)}}>Keep keeper, remove others</button></div>
      <p className="hint">Suggestions, not exact duplicates. Decisions apply to these shown photos and their stacked files. You can undo the choice.</p>
      {busy&&<ProgressBar label="Saving comparison choice"/>}
    </>}
  </div>;
}

function CompareImage({asset,zoom,pan,onPointerDown,onPointerMove,onPointerUp}:{asset:Asset;zoom:number;pan:Pan;onPointerDown:(event:PointerEvent<HTMLElement>)=>void;onPointerMove:(event:PointerEvent<HTMLElement>)=>void;onPointerUp:()=>void}){
  const [loaded,setLoaded]=useState(false),[failed,setFailed]=useState(false),[attempt,setAttempt]=useState(0);
  return <div className="compareimage" onPointerDown={onPointerDown} onPointerMove={onPointerMove} onPointerUp={onPointerUp} onPointerCancel={onPointerUp} onLostPointerCapture={onPointerUp}>
    {!loaded&&!failed&&<Busy label="Loading photo"/>}
    {failed?<div className="compareempty" role="status"><p>Preview unavailable for {nameOf(asset)}.</p><button className="btn" onClick={()=>{setFailed(false);setAttempt(value=>value+1)}}>Retry preview</button></div>:<img draggable={false} src={`/api/media/${asset.id}/preview?size=large${attempt?`&retry=${attempt}`:''}`} alt={nameOf(asset)} onLoad={()=>setLoaded(true)} onError={()=>setFailed(true)} style={{visibility:loaded?'visible':'hidden',transform:`scale(${zoom}) translate(${pan.x*100}%,${pan.y*100}%)`}}/>}
  </div>;
}
