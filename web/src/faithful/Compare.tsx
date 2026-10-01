import {useEffect,useRef,useState,type PointerEvent} from 'react';
import type {Asset} from '../api';

/** Two fixed-size stages share a normalised pan and magnification.
 * Decisions remain explicit, and the evidence stays beside the thumbnails. */
export function Compare({assets,focus,onFocus,onClose,onChoose,busy}:{assets:Asset[];focus:number;onFocus:(index:number)=>void;onClose:()=>void;onChoose:(mode:'keep-all'|'keep-focus'|'cull-all')=>void;busy:boolean}){
  const [zoom,setZoom]=useState(1);
  const [pan,setPan]=useState({x:0,y:0});
  const drag=useRef<{x:number;y:number;px:number;py:number}|null>(null);
  const reference=0,other=focus===reference?Math.min(1,assets.length-1):focus;
  const changeZoom=(value:number)=>{setZoom(Math.max(1,Math.min(8,value)));setPan({x:0,y:0})};
  useEffect(()=>{
    const keys=(event:KeyboardEvent)=>{if(event.key==='+'||event.key==='='){event.preventDefault();setZoom(value=>Math.min(8,value+1))}if(event.key==='-'){event.preventDefault();setZoom(value=>Math.max(1,value-1))}};
    window.addEventListener('keydown',keys);return()=>window.removeEventListener('keydown',keys);
  },[]);
  function start(event:PointerEvent<HTMLElement>){if(zoom===1)return;event.currentTarget.setPointerCapture(event.pointerId);drag.current={x:event.clientX,y:event.clientY,px:pan.x,py:pan.y}}
  function move(event:PointerEvent<HTMLElement>){const from=drag.current;if(!from)return;const box=event.currentTarget.getBoundingClientRect();setPan({x:Math.max(-.45,Math.min(.45,from.px+(event.clientX-from.x)/box.width/zoom)),y:Math.max(-.45,Math.min(.45,from.py+(event.clientY-from.y)/box.height/zoom))})}
  return <div className="rvcmp burstcompare" role="region" aria-label="Compare nearby photos">
    <div className="ctop"><b>Compare nearby photos</b><span>{focus+1} / {assets.length}</span><button className="btn" onClick={()=>changeZoom(zoom-1)} disabled={zoom===1} aria-label="Zoom out">−</button><span>{zoom}×</span><button className="btn" onClick={()=>changeZoom(zoom+1)} disabled={zoom===8} aria-label="Zoom in">+</button><button className="btn" onClick={()=>changeZoom(1)}>Fit</button><button className="rvx" aria-label="Close compare" onClick={onClose}>×</button></div>
    <p className="hint">Linked zoom and drag · 1–9 selects a photo · C closes. RAW and HEIC use a preview up to 2560 px.</p>
    <div className="comparepair">{[reference,other].map((index,side)=>{
      const asset=assets[index];
      return <div className={`compareframe${focus===index?' focused':''}`} key={side}>
        <div className="compareimage" onPointerDown={start} onPointerMove={move} onPointerUp={()=>{drag.current=null}} onPointerCancel={()=>{drag.current=null}}>
          <img draggable={false} src={`/api/media/${asset.id}/preview?size=large`} alt={asset.path.split('/').pop()} style={{transform:`scale(${zoom}) translate(${pan.x*100}%,${pan.y*100}%)`}}/>
        </div>
        <button className="textbtn" aria-pressed={focus===index} onClick={()=>onFocus(index)}>{asset.path.split('/').pop()}</button>
      </div>;
    })}</div>
    <div className="comparestrip" aria-label="Comparison candidates">{assets.map((asset,index)=><button className={focus===index?'on':''} aria-pressed={focus===index} key={asset.id} onClick={()=>onFocus(index)}><img src={`/api/media/${asset.id}/preview?size=grid`} alt=""/><b>{index+1}. {asset.path.split('/').pop()}</b><small>{asset.comparisonReason||'Related file'}</small></button>)}</div>
    <div className="cbot"><span className="hint">Suggestions only. Inspect every photo before removing it.</span><button className="rvbtn" disabled={busy} onClick={()=>onChoose('keep-all')}>Keep all</button><button className="rvbtn cull" disabled={busy} onClick={()=>onChoose('keep-focus')}>Keep selected, remove the rest</button></div>
  </div>;
}
