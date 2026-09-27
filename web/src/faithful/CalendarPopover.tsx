import {useEffect,type KeyboardEvent as ReactKeyboardEvent,type RefObject} from 'react';
import {Icon} from '../Icon';

// The two calendars that drop from the top bar, the streak's and the date
// pill's, share a frame: a month at a time, Monday first, and the manners of
// a popover.

export const weekdays=['M','T','W','T','F','S','S'];

/** The blank cells before the first of a month in a Monday-first week. */
export function leadDays(year:number,month:number){return (new Date(year,month,1).getDay()+6)%7}

/** Escape closes the calendar and hands focus back to the button that opened
 * it, and a press anywhere else closes it. Keys pressed inside stay inside,
 * so moving around a month never removes or favourites the photo selected
 * behind it. Spread the returned handler on the panel. */
export function usePopover(panel:RefObject<HTMLElement|null>,anchor:RefObject<HTMLElement|null>,onClose:()=>void){
  useEffect(()=>{
    const key=(event:KeyboardEvent)=>{if(event.key==='Escape'){event.preventDefault();onClose();anchor.current?.focus()}};
    const outside=(event:PointerEvent)=>{
      const target=event.target as Node;
      if(!panel.current?.contains(target)&&!anchor.current?.contains(target))onClose();
    };
    addEventListener('keydown',key);
    addEventListener('pointerdown',outside);
    return()=>{removeEventListener('keydown',key);removeEventListener('pointerdown',outside)};
  },[panel,anchor,onClose]);
  return {onKeyDown:(event:ReactKeyboardEvent)=>{if(event.key!=='Escape')event.stopPropagation()}};
}

/** The month's name between the arrows that step to the next and previous. */
export function MonthNav({title,sub,atStart=false,atEnd=false,onStep}:{title:string;sub?:string;atStart?:boolean;atEnd?:boolean;onStep:(by:number)=>void}){
  return <div className="calmonth">
    <button type="button" className="iconbtn" aria-label="Previous month" disabled={atStart} onClick={()=>onStep(-1)}><Icon name="arrow_back"/></button>
    <p aria-live="polite"><b>{title}</b>{sub&&<span>{sub}</span>}</p>
    <button type="button" className="iconbtn next" aria-label="Next month" disabled={atEnd} onClick={()=>onStep(1)}><Icon name="arrow_back"/></button>
  </div>;
}
