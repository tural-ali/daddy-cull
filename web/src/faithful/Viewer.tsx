import {useEffect,useLayoutEffect,useMemo,useRef,useState,type MouseEvent} from 'react';
import {Icon} from '../Icon';
import {binChanged,type Asset,type Status} from '../api';

function requestID(){return Array.from(crypto.getRandomValues(new Uint8Array(16)),byte=>byte.toString(16).padStart(2,'0')).join('')}
function preview(asset:Asset){return `/api/media/${asset.id}/preview?size=large`}
/** The file's format as Apple Photos badges it: RAW for any camera RAW, the
 * extension for everything else. */
function format(asset:Asset){
  const extension=asset.path.includes('.')?asset.path.split('.').pop()!.toUpperCase():'';
  return asset.kind==='raw'?'RAW':extension;
}

/** `dayOf`, when given, turns the date into a link to the file's own day, for
 * pages that show files from many days. */
/** `onMove` hears which photo is showing, so the address can follow it. */
export function Viewer({assets,initialID,onClose,onSave,onPatch,dayOf,onMove}:{assets:Asset[];initialID:number;onClose:()=>void;onSave:(asset:Asset,status:Status,favourite?:boolean)=>boolean;onPatch:(id:number,change:Partial<Asset>)=>void;dayOf?:(asset:Asset)=>string;onMove?:(id:number)=>void}){
  const initialIndex=Math.max(0,assets.findIndex(asset=>asset.id===initialID));
  const [at,setAt]=useState(initialIndex);
  // Info stays open from photo to photo, and from one visit to the next.
  const [info,setInfoState]=useState(()=>{try{return localStorage.getItem('cull-info')==='open'}catch{return false}});
  function setInfo(change:boolean|((open:boolean)=>boolean)){
    setInfoState(open=>{
      const next=typeof change==='function'?change(open):change;
      try{if(next)localStorage.setItem('cull-info','open');else localStorage.removeItem('cull-info')}catch{/* this visit only */}
      return next;
    });
  }
  const [help,setHelp]=useState(false);
  const [zoom,setZoom]=useState(false);
  const [bare,setBare]=useState(false);
  const [related,setRelated]=useState<Asset[]|null>(null);
  const [focus,setFocus]=useState(0);
  const [error,setError]=useState('');
  // Why the current file could not be drawn, keyed by its id so the next file
  // starts clean. A file moved off the archive between scans answers 404.
  const [broken,setBroken]=useState<{id:number;gone:boolean}|null>(null);
  const current=assets[Math.min(at,Math.max(0,assets.length-1))];
  const progress=assets.length?Math.round((at+1)/assets.length*100):0;
  const capture=current?.capturedAt?new Date(current.capturedAt*1000):null;
  const name=current?.path.split('/').pop()??'';
  const date=capture?.toLocaleDateString(undefined,{year:'numeric',month:'long',day:'numeric'})??'Date unknown';
  const time=capture?.toLocaleTimeString([],{hour:'2-digit',minute:'2-digit'})??'';
  const compareFiles=useMemo(()=>related??[],[related]);
  // The badge sits on the photograph's own top-left corner, which moves with
  // the picture's shape and the window, so it follows the drawn media.
  const media=useRef<HTMLImageElement&HTMLVideoElement>(null);
  const [corner,setCorner]=useState<{left:number;top:number}|null>(null);
  useLayoutEffect(()=>{
    const element=media.current;
    if(!element){setCorner(null);return}
    const place=()=>setCorner(element.offsetWidth>0?{left:element.offsetLeft,top:element.offsetTop}:null);
    place();
    const observer=new ResizeObserver(place);
    observer.observe(element);observer.observe(element.parentElement!);
    element.addEventListener('load',place);element.addEventListener('loadedmetadata',place);
    return()=>{observer.disconnect();element.removeEventListener('load',place);element.removeEventListener('loadedmetadata',place)};
  },[current?.id,zoom]);

  function failed(id:number){
    void fetch(`/api/media/${id}/original`,{method:'HEAD'}).then(response=>response.status===404,()=>false).then(gone=>setBroken({id,gone}));
  }
  function step(change:number){if(assets.length)setAt(index=>(index+change+assets.length)%assets.length);setZoom(false);setBare(false);setRelated(null)}
  function choose(status:Status,favourite?:boolean,advance=false){if(!current)return;if(onSave(current,status,favourite)&&advance)step(1)}
  async function openCompare(){
    if(!current||(current.relatedCount??0)<1)return;
    setError('');
    try{
      const response=await fetch(`/api/assets/${current.id}/related`);
      if(!response.ok)throw new Error('The related files could not be loaded.');
      const files:Asset[]=await response.json();
      setRelated(files);setFocus(Math.max(0,files.findIndex(file=>file.id===current.id)));
    }catch(reason){setError((reason as Error).message)}
  }
  async function saveGroup(mode:'keep-all'|'keep-focus'|'cull-all'){
    if(!related?.length)return;
    const jobs=related.map((asset,index)=>({assetId:asset.id,status:mode==='keep-all'?'keep':mode==='cull-all'?'cull':index===focus?'keep':'cull',favourite:asset.favourite,expectedRevision:asset.revision,requestId:requestID()}));
    const journal=`cull.group.pending.${requestID()}`;
    try{
      localStorage.setItem(journal,JSON.stringify(jobs));
      const response=await fetch('/api/decisions/batch',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(jobs)});
      if(!response.ok)throw new Error(response.status===409?'One file changed. Close and reopen the comparison.':'The group choice was retained locally but not confirmed.');
      const results:{revision:number}[]=await response.json();
      binChanged();
      jobs.forEach((job,index)=>onPatch(job.assetId,{status:job.status as Status,revision:results[index].revision}));
      localStorage.removeItem(journal);setRelated(null);step(1);
    }catch(reason){setError((reason as Error).message)}
  }

  const shownID=current?.id;
  useEffect(()=>{if(shownID!==undefined)onMove?.(shownID)},[shownID,onMove]);
  useEffect(()=>{
    document.documentElement.classList.add('rv-open');
    return()=>document.documentElement.classList.remove('rv-open');
  },[]);
  useEffect(()=>{
    const warmed:HTMLImageElement[]=[];
    for(let offset=-2;offset<=5;offset++){
      if(offset===0)continue;
      const asset=assets[at+offset];
      if(!asset||asset.kind==='video')continue;
      const image=new Image();image.src=preview(asset);warmed.push(image);
    }
    return()=>{warmed.forEach(image=>{image.src=''})};
  },[assets,at]);
  useEffect(()=>{
    function key(event:KeyboardEvent){
      if(event.ctrlKey||event.metaKey||event.altKey||event.target instanceof HTMLInputElement)return;
      const key=event.key.toLowerCase();
      if(['arrowright','arrowleft','x','k','f','i','z','c','g','escape',' ','?','1','2','3','4','5','6','7','8','9'].includes(key))event.preventDefault();
      if(related){
        if(/^[1-9]$/.test(key))setFocus(Math.min(Number(key)-1,related.length-1));
        else if(key==='c'||key==='escape')setRelated(null);
        else if(key==='x'){const asset=related[focus];if(asset)onSave(asset,asset.status==='cull'?'unreviewed':'cull')}
        return;
      }
      if(key==='arrowright')step(1);
      else if(key==='arrowleft')step(-1);
      else if(key==='x')choose(current.status==='cull'?'unreviewed':'cull',undefined,true);
      else if(key==='k')choose('keep',undefined,true);
      else if(key==='f')choose(current.status,!current.favourite);
      else if(key==='i')setInfo(value=>!value);
      else if(key==='z')setZoom(value=>!value);
      else if(key==='c')void openCompare();
      else if(key==='?')setHelp(value=>!value);
      else if(key==='g'||key==='escape'||key===' ')onClose();
    }
    window.addEventListener('keydown',key);
    return()=>window.removeEventListener('keydown',key);
  });
  if(!current)return null;
  // Clicking anywhere but the photograph or a control leaves the review, the
  // way a lightbox is expected to close.
  function outside(event:MouseEvent){
    if((event.target as HTMLElement).closest('img,video,button,a,input,.rvinfo,.rvcmp,.rvkeys,.toast'))return;
    onClose();
  }
  return <div className={`rv on${bare?' bare':''}${info?' info':''}${current.favourite?' isfav':''}${related?' cmp':''}`} role="dialog" aria-modal="true" aria-label="Photo review" onClick={outside}>
    <div className="rvtop">{dayOf?<a className="rvday" href={dayOf(current)} target="_blank" rel="noopener" title="Open this day in a new tab">{date}<Icon name="open_in_new"/></a>:<span className="rvday">{date}</span>}<span className="rvwhen">{time}</span><span className="rvpos">{at+1} / {assets.length}</span><button type="button" className="rvpath" aria-label="Copy file path" title={current.path} onClick={()=>void navigator.clipboard.writeText(current.path)}>📋</button><span className="rvbar"><span style={{width:`${progress}%`}}/></span><button type="button" className="rvx" aria-label="Close review" title="Close (Esc)" onClick={onClose}>×</button></div>
    <div className={`rvstage${zoom?' zoom':''}${current.kind==='video'?' hasvideo':''}`} onClick={event=>{if((event.target as HTMLElement).tagName==='IMG')setBare(value=>!value)}}>
      <button type="button" className="rvnav prev" aria-label="Previous" onClick={event=>{event.stopPropagation();step(-1)}}>‹</button>
      {broken?.id===current.id?<div className="rvgone" role="status"><b>{broken.gone?'This file is no longer in the archive':'This file could not be shown'}</b><span>{broken.gone?'It was moved or removed on the server since the last scan. It leaves review at the next nightly scan.':'Try again in a moment.'}</span></div>
        :current.kind==='video'?<video ref={media} key={current.id} controls autoPlay playsInline poster={preview(current)} src={`/api/media/${current.id}/original`} onError={()=>failed(current.id)}/>:<img ref={media} key={current.id} src={preview(current)} alt={name} onError={()=>failed(current.id)}/>}
      {corner&&!zoom&&format(current)&&<span className="rvformat" style={{left:corner.left+12,top:corner.top+12}} title={name.split('.').pop()?.toUpperCase()}>{format(current)}</span>}
      <button type="button" className="rvnav next" aria-label="Next" onClick={event=>{event.stopPropagation();step(1)}}>›</button>
    </div>
    <div className="rvbot"><button type="button" className="rvbtn cull" onClick={()=>choose(current.status==='cull'?'unreviewed':'cull',undefined,true)}><span className="ico">{current.status==='cull'?'↶':'🗑'}</span>{current.status==='cull'?'Undo remove':'Remove'} <kbd>X</kbd></button><button type="button" className={`rvbtn keep${current.status==='keep'?' on':''}`} onClick={()=>choose('keep',undefined,true)}><span className="ico">✓</span>{current.status==='keep'?'Kept':'Keep'} <kbd>K</kbd></button><button type="button" className={`rvbtn fav${current.favourite?' on':''}`} onClick={()=>choose(current.status,!current.favourite)}><span className="ico">{current.favourite?'★':'☆'}</span>{current.favourite?'Favourited':'Favourite'} <kbd>F</kbd></button>{(current.relatedCount??0)>0&&<button type="button" className="rvbtn cmp" onClick={()=>void openCompare()}>Compare <kbd>C</kbd></button>}<button type="button" className="rvbtn" onClick={()=>setInfo(value=>!value)}>Info <kbd>I</kbd></button></div>
    <aside className="rvinfo"><h3>Info</h3><dl><div><dt>File</dt><dd>{name}</dd></div><div><dt>Captured</dt><dd>{date} {time}</dd></div><div><dt>Type</dt><dd>{current.kind.toUpperCase()}</dd></div><div><dt>Size</dt><dd>{(current.size/1048576).toFixed(2)} MB</dd></div><div><dt>Decision</dt><dd>{current.status}</dd></div><div><dt>Path</dt><dd className="mono">{current.path}</dd></div></dl></aside>
    {related&&<div className="rvcmp"><div className="ctop"><b>Similar photos</b><span className="cpos">{focus+1} / {related.length}</span><span className="hint">1–9 focus a frame · X marks it · C back</span><button type="button" className="rvx cmpx" aria-label="Close compare" onClick={()=>setRelated(null)}>×</button></div><div className="cgrid">{compareFiles.map((asset,index)=><figure className={index===focus?'on':''} key={asset.id} onClick={()=>setFocus(index)}><img src={preview(asset)} alt={asset.path.split('/').pop()}/><span className="pick">{index+1}</span><figcaption>{asset.path.split('/').pop()} · {asset.status}</figcaption></figure>)}</div><div className="cfacts"><div className="verdict tied"><b>Possible copies or companion files</b><ul><li>Inspect before choosing</li><li>No file moves from this screen</li></ul></div></div><div className="cbot"><button type="button" className="rvbtn" onClick={()=>void saveGroup('keep-all')}>Keep all</button><button type="button" className="rvbtn cull" onClick={()=>void saveGroup('keep-focus')}>Keep the focused one, remove the rest</button><button type="button" className="rvbtn cull cmpall" onClick={()=>void saveGroup('cull-all')}>Remove all</button></div></div>}
    {help&&<div className="rvkeys" onClick={()=>setHelp(false)}><table><tbody><tr><td>→ ←</td><td>next / previous</td></tr><tr><td>K</td><td>keep and continue</td></tr><tr><td>X</td><td>remove, or undo a removal</td></tr><tr><td>F</td><td>favourite</td></tr><tr><td>I</td><td>info panel</td></tr><tr><td>Z</td><td>zoom</td></tr><tr><td>C</td><td>compare a group</td></tr><tr><td>G / Esc / Space</td><td>back to the grid</td></tr><tr><td>?</td><td>this list</td></tr></tbody></table></div>}
    {error&&<div className="toast err">{error}</div>}
  </div>;
}
