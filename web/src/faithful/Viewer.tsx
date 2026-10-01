import {useEffect,useEffectEvent,useLayoutEffect,useMemo,useRef,useState,type CSSProperties,type MouseEvent,type RefObject} from 'react';
import {SessionVideo} from '../SessionVideo';
import {Icon,type IconName} from '../Icon';
import {binChanged,type Asset,type FileDetails,type Status} from '../api';
import {tracked} from '../saving';
import {undoKeys,type HistoryEntry} from './history';
import {Kbd,tipProps} from './keys';
import {TurnedControls} from './TurnedControls';
import {useShownPath} from './libraryPath';
import {fileFormat as format} from './stacks';

function requestID(){return Array.from(crypto.getRandomValues(new Uint8Array(16)),byte=>byte.toString(16).padStart(2,'0')).join('')}
function preview(asset:Asset){return `/api/media/${asset.id}/preview?size=large`}

type Box={left:number;top:number;width:number;height:number};
/** The photograph's tile in the grid behind the viewer, when the page marks
 * its tiles with `data-asset`. */
function tileImage(id:number){return document.querySelector<HTMLImageElement>(`[data-asset="${id}"] img`)}
/** Where a picture of this shape sits when fitted to the stage, as the stage
 * will draw it. */
function fitted(stage:DOMRect,width:number,height:number):Box{
  const scale=Math.min(stage.width/width,stage.height/height,1);
  const w=width*scale,h=height*scale;
  return {left:stage.left+(stage.width-w)/2,top:stage.top+(stage.height-h)/2,width:w,height:h};
}
const box=(rect:DOMRect):Box=>({left:rect.left,top:rect.top,width:rect.width,height:rect.height});
const stillMotion=()=>matchMedia('(prefers-reduced-motion: reduce)').matches;
/** A picture in flight between its tile and the stage: the tile grows into
 * the photograph on opening, and the photograph shrinks back into its tile
 * on Back, as in Google Photos. */
type Flight={mode:'open'|'close';src:string;from:Box;to:Box;radius:string};
const flightTime=320;

/** `dayOf`, when given, turns the date into a link to the file's own day, for
 * pages that show files from many days. */
/** `onMove` hears which photo is showing, so the address can follow it. */
/** `onRecord`, when given, hears a group choice so the page can undo it. */
/** `behindOf` names the files behind a photo that stands for a RAW and its
 * exports, `onSeparate` shows them as separate photos when they turn out
 * not to belong together, and `onRemoveFormat` removes one of them alone. */
export function Viewer({assets,initialID,onClose,onSave,onPatch,dayOf,onMove,onRecord,behindOf,onSeparate,onRemoveFormat,onTurn}:{assets:Asset[];initialID:number;onClose:()=>void;onSave:(asset:Asset,status:Status,favourite?:boolean)=>boolean;onPatch:(id:number,change:Partial<Asset>)=>void;dayOf?:(asset:Asset)=>string;onMove?:(id:number)=>void;onRecord?:(entry:HistoryEntry)=>void;behindOf?:(asset:Asset)=>Asset[]|undefined;onSeparate?:(photo:Asset,files:Asset[])=>void;onRemoveFormat?:(photo:Asset,file:Asset)=>void;onTurn?:(asset:Asset,quarters:number)=>void}){
  const onDisk=useShownPath();
  const initialIndex=Math.max(0,assets.findIndex(asset=>asset.id===initialID));
  const [at,setAt]=useState(initialIndex);
  // The file the reviewer is on, or moving to. When the list changes under
  // the viewer (a stack split, or a choice that takes a file out of a filtered
  // list), the viewer stays with it rather than with a position.
  const anchor=useRef(initialID);
  useLayoutEffect(()=>{
    const index=assets.findIndex(asset=>asset.id===anchor.current);
    if(index>=0)setAt(index);
  },[assets]);
  // Which file of a stack is on the stage, the photo that stands for it first.
  const [side,setSide]=useState(0);
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
  const [menu,setMenu]=useState(false);
  // Remove on a photo of several files asks whether to remove the one on the
  // stage or all of them.
  const [ask,setAsk]=useState(false);
  const [zoom,setZoom]=useState(false);
  const [bare,setBare]=useState(false);
  const [related,setRelated]=useState<Asset[]|null>(null);
  const [focus,setFocus]=useState(0);
  const [error,setError]=useState('');
  const [copied,setCopied]=useState(false);
  // Why the current file could not be drawn, keyed by its id so the next file
  // starts clean. A file moved off the archive between scans answers 404.
  const [broken,setBroken]=useState<{id:number;gone:boolean}|null>(null);
  const current=assets[Math.min(at,Math.max(0,assets.length-1))];
  const others=current?behindOf?.(current):undefined;
  const stack=current&&others?[current,...others]:[];
  // What the stage draws: the photo, or a file behind it when asked for.
  const onStage=stack[side]??current;
  // A Live Photo plays its video over the still when asked, as a phone plays
  // it on a press, then shows the still again. Moving on stops it.
  const live=onStage?.live?onStage:current?.live?current:undefined;
  const [playing,setPlaying]=useState<number|null>(null);
  if(playing!==null&&playing!==live?.id)setPlaying(null);
  const livePlays=live!==undefined&&playing===live.id;
  const playLive=()=>{if(live)setPlaying(value=>value===live.id?null:live.id)};
  // How far the reviewer turned it in Cull; the file itself is as it was.
  const turn=onStage?.turn??0;
  // A picture zoomed while turned a quarter needs its own size to make room
  // for the turned shape; see .rvstage.zoom in the stylesheet.
  useLayoutEffect(()=>{
    const element=media.current;
    if(!(element instanceof HTMLImageElement)||!zoom||turn%2===0)return;
    const size=()=>{element.style.setProperty('--natural-w',`${element.naturalWidth}px`);element.style.setProperty('--natural-h',`${element.naturalHeight}px`)};
    if(element.complete)size();
    element.addEventListener('load',size);
    return()=>{element.removeEventListener('load',size);element.style.removeProperty('--natural-w');element.style.removeProperty('--natural-h')};
  },[zoom,turn,onStage?.id]);
  // Info describes the file on the stage, so switching to the RAW shows the
  // RAW's own name, size, camera and folder.
  const capture=onStage?.capturedAt?new Date(onStage.capturedAt*1000):null;
  const name=onStage?.path.split('/').pop()??'';
  const folder=onStage?.path.split('/').slice(0,-1).join('/')??'';
  // A file dated only by its folder is stored at midnight UTC: its day is
  // known and its time is not, so the day is read in UTC and no time is shown.
  const dayOnly=!!onStage?.capturedAt&&onStage.capturedAt%86400===0;
  // What each file records about itself, read once it is asked for.
  const [details,setDetails]=useState<ReadonlyMap<number,FileDetails|null>>(()=>new Map());
  const detailsID=info?onStage?.id:undefined;
  useEffect(()=>{
    if(detailsID===undefined||details.has(detailsID))return;
    const controller=new AbortController();
    fetch(`/api/assets/${detailsID}/details`,{signal:controller.signal})
      .then(response=>response.ok?response.json() as Promise<FileDetails>:null)
      .then(read=>setDetails(known=>new Map(known).set(detailsID,read)),()=>{/* aborted, or offline: read again when shown next */});
    return()=>controller.abort();
  },[detailsID,details]);
  const facts=onStage?details.get(onStage.id):undefined;
  const exposure=facts?[facts.aperture&&`ƒ/${facts.aperture}`,facts.shutter&&`${facts.shutter} s`,facts.focal&&`${facts.focal} mm`,facts.iso&&`ISO ${facts.iso}`].filter(Boolean).join(' · '):'';
  const width=facts?.width||onStage?.width,height=facts?.height||onStage?.height;
  // The prompt opens on the choice the reviewer is most likely after: the
  // file they switched to, or the whole photo while its face is showing.
  const askFirst=useRef<HTMLButtonElement>(null);
  useEffect(()=>{if(ask)askFirst.current?.focus()},[ask]);
  const zone=dayOnly?{timeZone:'UTC'}:{};
  const date=capture?.toLocaleDateString('en-GB',{year:'numeric',month:'long',day:'numeric',...zone})??'Date unknown';
  const weekday=capture?.toLocaleDateString('en-GB',{weekday:'short',...zone})??'';
  const time=dayOnly?'':capture?.toLocaleTimeString('en-GB',{hour:'2-digit',minute:'2-digit'})??'';
  const compareFiles=useMemo(()=>related??[],[related]);
  const media=useRef<HTMLImageElement&HTMLVideoElement>(null);
  const stage=useRef<HTMLDivElement>(null);

  // The opening flight: the tile's own thumbnail grows to where the large
  // preview will be drawn, and stays there until that preview has arrived.
  const [flight,setFlight]=useState<Flight|null>(null);
  const [landed,setLanded]=useState(false);
  const [shown,setShown]=useState<number|null>(null);
  const leaving=useRef(false);
  const flyer=useRef<HTMLImageElement>(null);
  // The photo the viewer opened on: the flight starts from its tile, however
  // far the review steps on from it.
  const [openedOn]=useState(initialID);
  const [openedAt]=useState(()=>performance.now());
  useLayoutEffect(()=>{
    const tile=tileImage(openedOn);
    const stageBox=stage.current?.getBoundingClientRect();
    // A turned picture fades in rather than flying, since the flyer is drawn upright.
    if(stillMotion()||!tile||!stageBox||!tile.naturalWidth||tile.getBoundingClientRect().width===0||tile.closest('[data-turn]'))return;
    const radius=getComputedStyle(tile.closest('figure')??tile).borderRadius;
    setFlight({mode:'open',src:tile.currentSrc||tile.src,from:box(tile.getBoundingClientRect()),to:fitted(stageBox,tile.naturalWidth,tile.naturalHeight),radius});
  },[openedOn]);
  const closed=useEffectEvent(()=>onClose());
  useEffect(()=>{
    const element=flyer.current;
    if(!flight||!element)return;
    const frame=(b:Box,radius:string)=>({left:`${b.left}px`,top:`${b.top}px`,width:`${b.width}px`,height:`${b.height}px`,borderRadius:radius});
    const animation=element.animate([frame(flight.from,flight.mode==='open'?flight.radius:'0px'),frame(flight.to,flight.mode==='open'?'0px':flight.radius)],{duration:flightTime,easing:'cubic-bezier(.2,0,0,1)',fill:'forwards'});
    let settled=false;
    const finish=()=>{if(settled)return;settled=true;if(flight.mode==='close')closed();else setLanded(true)};
    animation.onfinish=finish;
    // A tab in the background gets no animation frames; the flight still ends.
    const fallback=window.setTimeout(finish,flightTime+200);
    return()=>window.clearTimeout(fallback);
  },[flight]);
  // The thumbnail lifts once the large preview is drawn, or after a moment
  // when the preview is slow, so a stalled fetch never leaves it stuck.
  useEffect(()=>{
    if(!flight||flight.mode!=='open'||!landed)return;
    if(shown===openedOn||broken?.id===openedOn){setFlight(null);return}
    const timer=window.setTimeout(()=>setFlight(null),1500);
    return()=>window.clearTimeout(timer);
  },[flight,landed,shown,broken,openedOn]);
  /** Back: the photograph shrinks into its tile, then the viewer is gone. */
  function leave(){
    if(leaving.current)return;
    const element=media.current;
    const tile=current?tileImage(current.id):null;
    if(stillMotion()||!element||!tile||element.offsetWidth===0||turn){onClose();return}
    leaving.current=true;
    // The grid behind the viewer scrolls its tile into view, so the picture
    // lands where the reviewer will find it.
    tile.scrollIntoView({block:'center',behavior:'instant'});
    const radius=getComputedStyle(tile.closest('figure')??tile).borderRadius;
    const src=current.kind==='video'?preview(current):(element as HTMLImageElement).currentSrc||preview(current);
    setFlight({mode:'close',src,from:box(element.getBoundingClientRect()),to:box(tile.getBoundingClientRect()),radius});
  }

  function failed(id:number){
    void fetch(`/api/media/${id}/original`,{method:'HEAD'}).then(response=>response.status===404,()=>false).then(gone=>setBroken({id,gone}));
  }
  function step(change:number){
    if(assets.length){
      const next=(Math.min(at,assets.length-1)+change+assets.length)%assets.length;
      anchor.current=assets[next].id;
      setAt(next);
    }
    setZoom(false);setBare(false);setRelated(null);setMenu(false);setAsk(false);setSide(0);
  }
  function choose(status:Status,favoured?:boolean,advance=false){if(!current)return;if(onSave(current,status,favoured)&&advance)step(1)}
  // K and X each undo themselves and stay on the photo. A heart on a removed
  // photo brings it back, since the Bin never holds a favourite.
  function keep(){if(current)choose(current.status==='keep'?'unreviewed':'keep',undefined,current.status!=='keep')}
  function remove(){
    if(!current)return;
    if(others&&onRemoveFormat&&current.status!=='cull'){setMenu(false);setAsk(true);return}
    choose(current.status==='cull'?'unreviewed':'cull',undefined,current.status!=='cull');
  }
  /** Removes only the file on the stage. When it is the one standing for the
   * photo, the viewer moves to the next file of the stack, which stands for
   * what is left. */
  function removeShown(){
    setAsk(false);
    if(!current||!onRemoveFormat||!onStage)return;
    if(onStage===current&&stack[1])anchor.current=stack[1].id;
    setSide(0);
    onRemoveFormat(current,onStage);
  }
  function removeWhole(){setAsk(false);choose('cull',undefined,true)}
  function favourite(){if(current)choose(current.status==='cull'?'unreviewed':current.status,!current.favourite)}
  function copyPath(){
    if(!current)return;
    navigator.clipboard.writeText(onDisk((onStage??current).path)).then(()=>{setCopied(true);window.setTimeout(()=>setCopied(false),1500)},()=>setError('The path could not be copied.'));
  }
  // A stack's own files are not similar photos to compare with.
  const similar=(current?.relatedCount??0)-(others?.length??0);
  async function openCompare(){
    if(!current||similar<1)return;
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
    const jobs=related.map((asset,index)=>({assetId:asset.id,status:mode==='keep-all'?'keep':mode==='cull-all'?'cull':index===focus?'keep':'cull',favourite:mode==='cull-all'||(mode==='keep-focus'&&index!==focus)?false:asset.favourite,expectedRevision:asset.revision,requestId:requestID()}));
    const journal=`cull.group.pending.${requestID()}`;
    try{
      localStorage.setItem(journal,JSON.stringify(jobs));
      const response=await tracked(fetch('/api/decisions/batch',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(jobs)}));
      if(!response.ok)throw new Error(response.status===409?'One file changed. Close and reopen the comparison.':'The group choice was retained locally but not confirmed.');
      const results:{revision:number}[]=await response.json();
      binChanged();
      jobs.forEach((job,index)=>onPatch(job.assetId,{status:job.status as Status,favourite:job.favourite,revision:results[index].revision}));
      onRecord?.({kind:'decisions',label:`chose among ${related.length} similar photos`,before:related.map(asset=>({id:asset.id,status:asset.status,favourite:asset.favourite})),after:jobs.map(job=>({id:job.assetId,status:job.status as Status,favourite:job.favourite}))});
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
    function onKey(event:KeyboardEvent){
      if(event.ctrlKey||event.metaKey||event.altKey||event.target instanceof HTMLInputElement)return;
      // The key that opened the viewer is still on its way up to the window;
      // Space opens a tile and closes the viewer, so it would do both. Both
      // clocks are coarse, so the same tick counts as before.
      if(event.timeStamp<=openedAt)return;
      const key=event.key.toLowerCase();
      if(['arrowright','arrowleft','x','k','f','i','z','c','r','l','g','h','escape',' ','?','[',']','1','2','3','4','5','6','7','8','9'].includes(key))event.preventDefault();
      if(menu){if(key==='escape')setMenu(false);return}
      if(ask){
        if(key==='escape')setAsk(false);
        else if(key==='o')removeShown();
        else if(key==='x')removeWhole();
        else if(key==='arrowdown'||key==='arrowup'){
          event.preventDefault();
          const items=[...document.querySelectorAll<HTMLButtonElement>('.rvask [role="menuitem"]')];
          const focused=items.indexOf(document.activeElement as HTMLButtonElement);
          items[(focused+(key==='arrowdown'?1:items.length-1))%items.length]?.focus();
        }
        return;
      }
      if(help&&key==='escape'){setHelp(false);return}
      if(related){
        if(/^[1-9]$/.test(key))setFocus(Math.min(Number(key)-1,related.length-1));
        else if(key==='c'||key==='escape')setRelated(null);
        else if(key==='x'){const asset=related[focus];if(asset)onSave(asset,asset.status==='cull'?'unreviewed':'cull')}
        return;
      }
      if(key==='arrowright')step(1);
      else if(key==='arrowleft')step(-1);
      else if(key==='x')remove();
      else if(key==='k')keep();
      else if(key==='f')favourite();
      else if(key==='i')setInfo(value=>!value);
      else if(key==='z')setZoom(value=>!value);
      else if(key==='l')playLive();
      else if(key==='h')setBare(value=>!value);
      else if(key==='c')void openCompare();
      else if(key==='r'&&others)setSide(value=>(value+1)%stack.length);
      else if(key===']'&&onTurn)onTurn(current,1);
      else if(key==='['&&onTurn)onTurn(current,-1);
      else if(key==='?')setHelp(value=>!value);
      else if(key==='g'||key==='escape'||key===' ')leave();
    }
    window.addEventListener('keydown',onKey);
    return()=>window.removeEventListener('keydown',onKey);
  });
  if(!current)return null;
  // Clicking anywhere but the photograph or a control leaves the review, the
  // way a lightbox is expected to close.
  function outside(event:MouseEvent){
    if(menu){setMenu(false);return}
    if(ask){setAsk(false);return}
    if((event.target as HTMLElement).closest('img,video,button,a,input,.rvinfo,.rvcmp,.rvkeys,.toast'))return;
    leave();
  }
  const mediaIcon:IconName=onStage.kind==='video'?'videocam':onStage.kind==='raw'?'raw_on':'image';
  const megabytes=(file:Asset)=>`${(file.size/1048576).toFixed(1)} MB`;
  const decision=current.status==='keep'?{icon:'check_circle' as IconName,text:'Kept'}
    :current.status==='cull'?{icon:'delete' as IconName,text:'Marked for the Bin'}
    :{icon:'schedule' as IconName,text:'Not decided yet'};
  // oxlint-disable-next-line jsx-a11y/click-events-have-key-events, jsx-a11y/no-noninteractive-element-interactions -- a click outside the photo is the mouse's Esc
  return <div className={`rv on${bare?' bare':''}${info?' info':''}${related?' cmp':''}${flight?` flight ${flight.mode}`:''}`} role="dialog" aria-modal="true" aria-label="Photo review" onClick={outside}>
    <div className="rvbody">
    {/* oxlint-disable-next-line jsx-a11y/no-static-element-interactions -- drops the focus ring a mouse click leaves on a button; keys never need it */}
    <div className="rvtop" onMouseUp={event=>(event.target as HTMLElement).closest('button')?.blur()}>
      <button type="button" className="rvact rvback" aria-label="Back to the grid" {...tipProps('Back','Escape')} onClick={leave}><Icon name="arrow_back"/></button>
      <div className="rvacts">
        {others?<div className="rvpair" role="group" aria-label="Show which file of this photo" {...tipProps('Each file of this photo','R')}>
          {stack.map((file,index)=><button type="button" key={file.id} aria-pressed={index===side} title={file.path.split('/').pop()} onClick={()=>setSide(index)}>{format(file)}</button>)}
        </div>:format(current)&&<span className="rvformat" title={name.split('.').pop()?.toUpperCase()}>{format(current)}</span>}
        {live&&<button type="button" className={`rvact live${livePlays?' on':''}`} aria-label={livePlays?'Stop the Live Photo':'Play the Live Photo'} aria-pressed={livePlays} {...tipProps(livePlays?'Stop':'Play Live Photo','L')} onClick={playLive}><Icon name="motion_photos_on" filled={livePlays}/></button>}
        {similar>0&&<button type="button" className="rvact cmp" aria-label="Compare similar photos" {...tipProps('Compare','C')} onClick={()=>void openCompare()}><Icon name="compare"/></button>}
        {onTurn&&<button type="button" className="rvact turn" aria-label="Rotate clockwise" {...tipProps('Rotate clockwise',']')} onClick={()=>onTurn(current,1)}><Icon name="rotate_right"/></button>}
        <button type="button" className="rvact zoom" aria-label="Zoom" aria-pressed={zoom} {...tipProps('Zoom','Z')} onClick={()=>setZoom(value=>!value)}><Icon name="zoom_in" filled={zoom}/></button>
        <button type="button" className="rvact infobtn" aria-label="Info" aria-pressed={info} {...tipProps('Info','I')} onClick={()=>setInfo(value=>!value)}><Icon name="info" filled={info}/></button>
        <button type="button" className={`rvact fav${current.favourite?' on':''}`} aria-label={current.favourite?'Favourited':'Favourite'} aria-pressed={current.favourite} {...tipProps(current.favourite?'Favourited, again to undo':'Favourite','F')} onClick={favourite}><Icon name="favorite" filled={current.favourite}/></button>
        <button type="button" className={`rvact keep${current.status==='keep'?' on':''}`} aria-label={current.status==='keep'?'Kept':'Keep'} aria-pressed={current.status==='keep'} {...tipProps(current.status==='keep'?'Kept, again to undo':'Keep','K')} onClick={keep}><Icon name="check_circle" filled={current.status==='keep'}/></button>
        <button type="button" className={`rvact cull${current.status==='cull'?' on':''}`} aria-label={current.status==='cull'?'Undo remove':'Remove'} aria-pressed={current.status==='cull'} {...tipProps(current.status==='cull'?'Marked for the Bin, again to undo':'Remove','X')} onClick={remove}><Icon name={current.status==='cull'?'restore_from_trash':'delete'}/></button>
        {/* oxlint-disable-next-line jsx-a11y/click-events-have-key-events -- keeps a click inside the prompt from closing the viewer; Esc closes the prompt */}
        {ask&&<div className="rvmenu rvask" role="menu" aria-label="Remove which files?" tabIndex={-1} onClick={event=>event.stopPropagation()}>
          <p>This photo is {stack.length} files: {stack.map(format).join(', ').replace(/, ([^,]*)$/,' and $1')}.</p>
          <button type="button" role="menuitem" ref={side>0?askFirst:undefined} onClick={removeShown}><Icon name="delete"/><span><b>Only the {format(onStage)}</b><small>{name} · {megabytes(onStage)}</small></span><Kbd keys="O"/></button>
          <button type="button" role="menuitem" ref={side>0?undefined:askFirst} onClick={removeWhole}><Icon name="delete"/><span><b>The whole photo</b><small>All {stack.length} files · {(stack.reduce((sum,file)=>sum+file.size,0)/1048576).toFixed(1)} MB</small></span><Kbd keys="X"/></button>
        </div>}
        <button type="button" className="rvact" aria-label="More" aria-haspopup="menu" aria-expanded={menu} {...tipProps('More')} onClick={event=>{event.stopPropagation();setMenu(value=>!value)}}><Icon name="more_vert"/></button>
        {/* oxlint-disable-next-line jsx-a11y/click-events-have-key-events -- keeps a click inside the menu from closing the viewer; Esc closes the menu */}
        {menu&&<div className="rvmenu" role="menu" tabIndex={-1} onClick={event=>event.stopPropagation()}>
          {dayOf&&<a role="menuitem" href={dayOf(current)} target="_blank" rel="noopener" onClick={()=>setMenu(false)}><Icon name="open_in_new"/>Open this day in a new tab</a>}
          <button type="button" role="menuitem" onClick={()=>{setMenu(false);copyPath()}}><Icon name="content_copy"/>Copy file path</button>
          {others&&onSeparate&&<button type="button" role="menuitem" onClick={()=>{setMenu(false);setSide(0);onSeparate(current,others)}}><Icon name="link_off"/>Show the {stack.map(format).join(', ').replace(/, ([^,]*)$/,' and $1')} separately</button>}
          <button type="button" role="menuitem" onClick={()=>{setMenu(false);setHelp(true)}}><Icon name="keyboard"/>Keyboard shortcuts<Kbd keys="?"/></button>
        </div>}
      </div>
    </div>
    {/* The stage's children are keyed by the file they show, so each sort has
        a key of its own: a clip and its controls, or a still and its Live
        Photo video, sharing one would leave the clip behind on moving on. */}
    {/* oxlint-disable-next-line jsx-a11y/click-events-have-key-events, jsx-a11y/no-static-element-interactions -- clicking the photo hides the controls; H does the same */}
    <div ref={stage} className={`rvstage${zoom?' zoom':''}${current.kind==='video'?' hasvideo':''}`} onClick={event=>{if((event.target as HTMLElement).tagName==='IMG')setBare(value=>!value)}}>
      <button type="button" className="rvnav prev" aria-label="Previous" {...tipProps('Previous','ArrowLeft')} onClick={event=>{event.stopPropagation();step(-1)}}>‹</button>
      {broken?.id===onStage.id?<div className="rvgone" role="status"><b>{broken.gone?'This file is no longer in the archive':'This file could not be shown'}</b><span>{broken.gone?'It was moved or removed on the server since the last scan. It leaves review at the next nightly scan.':'Try again in a moment.'}</span></div>
        :current.kind==='video'?<SessionVideo ref={media} key={current.id} data-turn={turn||undefined} controls={!turn} autoPlay playsInline poster={preview(current)} src={`/api/media/${current.id}/original`} onLoadedData={()=>setShown(current.id)} onError={()=>failed(current.id)}/>:<img ref={media} key={onStage.id} data-turn={turn||undefined} src={preview(onStage)} alt={onStage.path.split('/').pop()} onLoad={()=>setShown(current.id)} onError={()=>failed(onStage.id)}/>}
      {live&&livePlays&&current.kind!=='video'&&broken?.id!==onStage.id&&<LiveClip key={`live:${live.id}`} src={`/api/media/${live.id}/live`} still={media} turn={turn} onDone={()=>setPlaying(null)} onFail={()=>{setPlaying(null);setError('The Live Photo video could not be played.')}}/>}
      {current.kind==='video'&&turn!==0&&broken?.id!==onStage.id&&<TurnedControls key={`controls:${current.id}`} video={media}/>}
      <button type="button" className="rvnav next" aria-label="Next" {...tipProps('Next','ArrowRight')} onClick={event=>{event.stopPropagation();step(1)}}>›</button>
    </div>
    <aside className="rvinfo" aria-label="Info">
      <div className="ihead"><button type="button" className="rvact" aria-label="Close info" {...tipProps('Close','I')} onClick={()=>setInfo(false)}><Icon name="close"/></button><h3>Info</h3></div>
      <h4>Details</h4>
      <div className="irow"><Icon name="calendar_month"/><div><b>{date}</b><span>{weekday}{weekday&&time?', ':''}{time}</span></div></div>
      <div className="irow"><Icon name={mediaIcon}/><div><b>{name}</b><span>{[megabytes(onStage),format(onStage)||onStage.kind.toUpperCase(),width&&height?`${width} × ${height}`:''].filter(Boolean).join(' · ')}</span></div></div>
      {facts?.camera&&<div className="irow"><Icon name="photo_camera"/><div><b>{facts.camera}</b>{exposure&&<span>{exposure}</span>}{facts.lens&&<span>{facts.lens}</span>}</div></div>}
      {facts&&<div className="irow"><Icon name="edit"/><div><b>{facts.software||'Software not recorded'}</b><span>File last saved {new Date(facts.modified).toLocaleDateString('en-GB',{day:'numeric',month:'long',year:'numeric'})}</span></div></div>}
      {facts&&<div className="irow"><Icon name={facts.located?'location_on':'location_off'}/><div><b>{facts.located?'Records where it was taken':'No location recorded'}</b></div></div>}
      <div className="irow"><Icon name={decision.icon}/><div><b>{decision.text}</b><span>{current.favourite?'Favourite · ':''}<span className="rvpos">{at+1} / {assets.length}</span> in this review</span></div></div>
      <div className="irow"><Icon name="folder"/><div><b>{folder.split('/').pop()||folder}</b><span className="mono">{onDisk(onStage.path)}</span></div><button type="button" className="rvact copy" aria-label="Copy file path" {...tipProps(copied?'Copied':'Copy file path')} onClick={copyPath}><Icon name={copied?'check':'content_copy'}/></button></div>
      {others&&<>
        <h4>Files of this photo</h4>
        <p className="ihint">Keep acts on every file. Remove asks whether to take only the one shown.</p>
        {stack.map((file,index)=><button type="button" className="irow ifile" key={file.id} aria-pressed={index===side} onClick={()=>setSide(index)} onMouseUp={event=>event.currentTarget.blur()}><Icon name={file.kind==='raw'?'raw_on':'image'}/><div><b>{file.path.split('/').pop()}</b><span>{megabytes(file)} · {format(file)}{index===side?', shown':''}</span></div></button>)}
      </>}
    </aside>
    {related&&<div className="rvcmp"><div className="ctop"><b>Similar photos</b><span className="cpos">{focus+1} / {related.length}</span><span className="hint">1–9 focus a frame · X marks it · C back</span><button type="button" className="rvx cmpx" aria-label="Close compare" {...tipProps('Close','C')} onClick={()=>setRelated(null)}>×</button></div><div className="cgrid">{compareFiles.map((asset,index)=>
      // oxlint-disable-next-line jsx-a11y/click-events-have-key-events, jsx-a11y/no-noninteractive-element-interactions -- 1 to 9 focus a frame from the keyboard
      <figure className={index===focus?'on':''} key={asset.id} onClick={()=>setFocus(index)}><img src={preview(asset)} alt={asset.path.split('/').pop()}/><span className="pick">{index+1}</span><figcaption>{asset.path.split('/').pop()} · {asset.status}</figcaption></figure>)}</div><div className="cfacts"><div className="verdict tied"><b>Possible copies or companion files</b><ul><li>Inspect before choosing</li><li>No file moves from this screen</li></ul></div></div><div className="cbot"><button type="button" className="rvbtn" onClick={()=>void saveGroup('keep-all')}>Keep all</button><button type="button" className="rvbtn cull" onClick={()=>void saveGroup('keep-focus')}>Keep the focused one, remove the rest</button><button type="button" className="rvbtn cull cmpall" onClick={()=>void saveGroup('cull-all')}>Remove all</button></div></div>}
    {/* oxlint-disable-next-line jsx-a11y/click-events-have-key-events, jsx-a11y/no-static-element-interactions -- a click dismisses the sheet; ? and Esc do the same */}
    {help&&<div className="rvkeys" onClick={()=>setHelp(false)}><table><tbody><tr><td>→ ←</td><td>next / previous</td></tr><tr><td>K</td><td>keep and continue, again to undo</td></tr><tr><td>X</td><td>remove (clears keep and favourite), again to undo</td></tr>{others&&onRemoveFormat&&<tr><td>X then O / X</td><td>remove only the file shown / the whole photo</td></tr>}<tr><td>F</td><td>favourite</td></tr><tr><td>I</td><td>info panel</td></tr><tr><td>Z</td><td>zoom</td></tr>{live&&<tr><td>L</td><td>play the Live Photo, again to stop</td></tr>}{onTurn&&<tr><td>] / [</td><td>rotate clockwise / anticlockwise, in Cull only</td></tr>}<tr><td>H</td><td>hide the controls, again to show them</td></tr><tr><td>C</td><td>compare a group</td></tr>{others&&<tr><td>R</td><td>show the next file of this photo, such as its RAW</td></tr>}<tr><td>{undoKeys.undo} / {undoKeys.redo}</td><td>{onRecord?'undo / redo the last choice':'undo / redo, on the day page'}</td></tr><tr><td>G / Esc / Space</td><td>back to the grid</td></tr><tr><td>?</td><td>this list</td></tr></tbody></table></div>}
    {error&&<div className="toast err">{error}</div>}
    </div>
    {flight&&<img ref={flyer} className="rvfly" src={flight.src} alt="" style={{left:flight.from.left,top:flight.from.top,width:flight.from.width,height:flight.from.height,borderRadius:flight.mode==='open'?flight.radius:0}}/>}
  </div>;
}

// LiveClip plays a Live Photo's video over its still, at the still's size and
// turn, with sound, as a phone plays it on a press. A click stops it early.
function LiveClip({src,still,turn,onDone,onFail}:{src:string;still:RefObject<HTMLElement|null>;turn:number;onDone:()=>void;onFail:()=>void}){
  const [frame,setFrame]=useState<CSSProperties|null>(null);
  useLayoutEffect(()=>{
    const image=still.current;
    if(!image)return;
    const place=()=>setFrame({left:image.offsetLeft,top:image.offsetTop,width:image.offsetWidth,height:image.offsetHeight});
    place();
    const observer=new ResizeObserver(place);
    observer.observe(image);
    return()=>observer.disconnect();
  },[still]);
  // oxlint-disable-next-line jsx-a11y/media-has-caption -- a Live Photo is a few seconds of the moment, with no words to caption
  return <video className="rvlive" style={frame??{visibility:'hidden'}} data-turn={turn||undefined} src={src} autoPlay playsInline onEnded={onDone} onError={onFail} onClick={event=>{event.stopPropagation();onDone()}}/>;
}
