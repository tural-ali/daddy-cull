import {useEffect,useState,type MouseEvent,type ReactNode} from 'react';
import {SessionVideo} from '../SessionVideo';
import {Icon} from '../Icon';
import {dayName} from './goto';
import {matches,tipProps} from './keys';

/** One file shown full size. `base` is a media route that answers
 * `/preview?size=large` for a still and `/original` for a clip. */
export type LightboxItem={key:string;base:string;name:string;kind:string;detail?:string;day?:string};

// Lightbox shows files that are not, or are no longer, catalogue decisions: what
// sits in the Bin or waits out its grace period after being deleted from it. It
// looks and steps like the review viewer, but offers only the actions its page
// passes in, because deciding keep or remove means nothing for these files.
export function Lightbox({items,initialKey,onClose,renderActions,onMove}:{items:LightboxItem[];initialKey:string;onClose:()=>void;renderActions?:(item:LightboxItem)=>ReactNode;onMove?:(key:string)=>void}){
  const [at,setAt]=useState(()=>Math.max(0,items.findIndex(item=>item.key===initialKey)));
  const current=items[Math.min(at,items.length-1)];
  const shownKey=current?.key;
  useEffect(()=>{if(shownKey!==undefined)onMove?.(shownKey)},[shownKey,onMove]);
  function step(change:number){if(items.length)setAt(index=>(index+change+items.length)%items.length)}
  useEffect(()=>{
    document.documentElement.classList.add('rv-open');
    return()=>document.documentElement.classList.remove('rv-open');
  },[]);
  useEffect(()=>{
    // The list shrinks when the page acts on a file, so the position is kept
    // inside it, and an emptied list closes the lightbox.
    if(items.length===0)onClose();
    else if(at>=items.length)setAt(items.length-1);
  },[items.length,at,onClose]);
  useEffect(()=>{
    function key(event:KeyboardEvent){
      if(event.ctrlKey||event.metaKey||event.altKey)return;
      if(event.key==='ArrowRight'){event.preventDefault();step(1)}
      else if(event.key==='ArrowLeft'){event.preventDefault();step(-1)}
      else if(event.key==='Escape'){event.preventDefault();onClose()}
      else{
        // The page's actions under the photo carry their own keys.
        const action=[...document.querySelectorAll<HTMLButtonElement>('.lb .rvbot button[data-keys]')].find(button=>matches(event,button.dataset.keys!));
        if(action&&!event.repeat){event.preventDefault();if(!action.disabled)action.click()}
      }
    }
    window.addEventListener('keydown',key);
    return()=>window.removeEventListener('keydown',key);
  });
  if(!current)return null;
  function outside(event:MouseEvent){
    if((event.target as HTMLElement).closest('img,video,button,a'))return;
    onClose();
  }
  const still=`${current.base}/preview?size=large`;
  // oxlint-disable-next-line jsx-a11y/click-events-have-key-events, jsx-a11y/no-noninteractive-element-interactions -- a click outside the photo is the mouse's Esc
  return <div className="rv on lb" role="dialog" aria-modal="true" aria-label={`Preview of ${current.name}`} onClick={outside}>
    <div className="rvbody">
    <div className="rvtop">
      <button type="button" className="rvact rvback" aria-label="Close" {...tipProps('Close','Escape')} onClick={onClose}><Icon name="arrow_back"/></button>
      <div className="lbtitle"><b>{current.name}</b><span>{current.detail}{current.day&&<a href={current.day} target="_blank" rel="noopener" title="See what else is filed on this day, in a new tab">{dayName(current.day)}<Icon name="open_in_new"/></a>}</span></div>
      {items.length>1&&<span className="rvpos">{at+1} / {items.length}</span>}
    </div>
    <div className={`rvstage${current.kind==='video'?' hasvideo':''}`}>
      {items.length>1&&<button type="button" className="rvnav prev" aria-label="Previous" {...tipProps('Previous','ArrowLeft')} onClick={event=>{event.stopPropagation();step(-1)}}>‹</button>}
      {current.kind==='video'?<SessionVideo key={current.key} controls autoPlay playsInline poster={still} src={`${current.base}/original`}/>:<img key={current.key} src={still} alt={current.name}/>}
      {items.length>1&&<button type="button" className="rvnav next" aria-label="Next" {...tipProps('Next','ArrowRight')} onClick={event=>{event.stopPropagation();step(1)}}>›</button>}
    </div>
    {renderActions&&<div className="rvbot">{renderActions(current)}</div>}
    </div>
  </div>;
}
