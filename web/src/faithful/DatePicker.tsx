import {createContext,useContext,useEffect,useLayoutEffect,useRef,useState,type KeyboardEvent,type RefObject} from 'react';
import {Icon} from '../Icon';
import {calendarLabel,type CalendarCell,type YearData} from './Year';
import {leadDays,MonthNav,usePopover,weekdays} from './CalendarPopover';

// The date a page is about sits in the search bar as a pill, the way Google
// Photos shows what a search is narrowed to. The pill opens a calendar of the
// year's dates, each drawn by how far its review has got, so another date is
// one press away and the pages either side are in the calendar's footer.

/** `md` is the month and day the page shows across every year. */
export type PageDate={md:string;label:string;short:string;done:boolean;years:number;yearsDone:number;previous:string;next:string};

const zone=Intl.DateTimeFormat().resolvedOptions().timeZone;

const PageDateContext=createContext<(date:PageDate|null)=>void>(()=>{});
export const PageDateProvider=PageDateContext.Provider;

/** Puts the page's date in the search bar, or takes it away with null. */
export function usePageDate(date:PageDate|null){
  const set=useContext(PageDateContext);
  const latest=useRef(date);
  latest.current=date;
  const signature=date?JSON.stringify(date):'';
  useLayoutEffect(()=>{set(latest.current)},[signature,set]);
  useEffect(()=>()=>set(null),[set]);
}

function two(value:number){return String(value).padStart(2,'0')}
function monthDay(date:Date){return `${two(date.getMonth()+1)}-${two(date.getDate())}`}
/** A date's month and day moved by a number of days. 2000 is a leap year, so
 * 29 February has neighbours. */
function shift(md:string,by:number){
  const [month,day]=md.split('-').map(Number);
  return monthDay(new Date(2000,month-1,day+by));
}
const shortMonths=['Jan','Feb','Mar','Apr','May','Jun','Jul','Aug','Sep','Oct','Nov','Dec'];
/** "7 Sep" for a month and day, with a year when one is given. The browser's
 * own short months say "Sept" in British English, which reads oddly beside
 * the other three-letter months. */
export function shortLabel(md:string,year?:string){
  const [month,day]=md.split('-').map(Number);
  return `${day} ${shortMonths[month-1]}${year?` ${year}`:''}`;
}
function plural(count:number,word:string){return `${count.toLocaleString()} ${word}${count===1?'':'s'}`}

export function DatePill({date,pill,open,onToggle}:{date:PageDate;pill:RefObject<HTMLButtonElement|null>;open:boolean;onToggle:()=>void}){
  const today=date.md===monthDay(new Date());
  const label=`${date.label}${today?', today':''}${date.done?', reviewed':''}. Choose another date`;
  return <button ref={pill} type="button" className={`datepill${date.done?' done':''}`} aria-haspopup="dialog" aria-expanded={open} aria-label={label} title={label} onClick={onToggle}>
    <Icon name={date.done?'check_circle':'today'} filled={date.done}/>
    <span className="long">{date.label}</span><span className="short">{date.short}</span>
    <span className="drop"><Icon name="arrow_drop_down"/></span>
  </button>;
}

/** The year's dates a month at a time, laid out on this year's weeks. A
 * reviewed date is a disc, a partly reviewed one carries a dot, and a date
 * with nothing filed under it is faint. The arrow keys walk the days and run
 * on into the months either side. */
export function DateCalendar({date,anchor,onClose}:{date:PageDate;anchor:RefObject<HTMLButtonElement|null>;onClose:()=>void}){
  const [year,setYear]=useState<YearData|null>(null);
  const [failed,setFailed]=useState(false);
  const [attempt,setAttempt]=useState(0);
  const [month,setMonth]=useState(Number(date.md.slice(0,2))-1);
  const [focus,setFocus]=useState(date.md);
  const moved=useRef(true);
  const panel=useRef<HTMLDivElement>(null);
  const grid=useRef<HTMLDivElement>(null);
  const keys=usePopover(panel,anchor,onClose);
  useEffect(()=>{
    const controller=new AbortController();
    setFailed(false);
    fetch(`/api/year?tz=${encodeURIComponent(zone)}`,{signal:controller.signal}).then(response=>{
      if(!response.ok)throw new Error();
      return response.json() as Promise<YearData>;
    }).then(setYear).catch(()=>{if(!controller.signal.aborted)setFailed(true)});
    return()=>controller.abort();
  },[attempt]);
  useEffect(()=>{
    // The page's own date takes focus on opening, and a moved focus follows
    // the arrow keys once the month they ran into has drawn.
    if(!moved.current)return;
    moved.current=false;
    grid.current?.querySelector<HTMLElement>(`[data-md="${focus}"]`)?.focus({preventScroll:true});
  },[focus,month]);
  const now=new Date();
  const today=monthDay(now);
  const layoutYear=now.getFullYear();
  const count=new Date(2000,month+1,0).getDate();
  const known=new Map((year?.months[month]?.cells??[]).filter((cell):cell is CalendarCell=>cell!==null).map(cell=>[cell.md,cell]));
  const days=Array.from({length:count},(_,index)=>{const md=`${two(month+1)}-${two(index+1)}`;return {md,dom:index+1,cell:known.get(md)}});
  const filed=days.filter(day=>(day.cell?.years??0)>0);
  const reviewed=filed.filter(day=>day.cell?.state==='done').length;
  const monthName=new Date(2000,month,1).toLocaleDateString('en-GB',{month:'long'});
  const step=(by:number)=>{setMonth(current=>(current+by+12)%12)};
  function walk(event:KeyboardEvent){
    const by={ArrowLeft:-1,ArrowRight:1,ArrowUp:-7,ArrowDown:7}[event.key];
    if(by===undefined)return;
    event.preventDefault();
    const next=shift(focus,by);
    moved.current=true;
    setMonth(Number(next.slice(0,2))-1);
    setFocus(next);
  }
  function describe(md:string,cell?:CalendarCell){
    const parts=[calendarLabel(md)];
    if(md===today)parts.push('today');
    if(md===date.md)parts.push('showing now');
    if(cell){
      if(cell.years===0)parts.push('nothing filed');
      else if(cell.state==='done')parts.push('reviewed');
      else parts.push(`${plural(cell.waiting,'file')} waiting`);
      if((cell.fresh??0)>0)parts.push(`${plural(cell.fresh!,'newly arrived file')}`);
    }
    return parts.join(', ');
  }
  const heading=date.md===today?'Today.':'';
  const progress=date.years===0?'Nothing in the archive is filed under this date.'
    :date.done?(date.years===1?'Reviewed.':`All ${date.years.toLocaleString()} years reviewed.`)
    :`${date.yearsDone.toLocaleString()} of ${plural(date.years,'year')} reviewed.`;
  const focusable=days.some(day=>day.md===focus)?focus:days[0].md;
  return <div ref={panel} className="calpop datecal" role="dialog" aria-label="Choose a date" tabIndex={-1} {...keys}>
    <div className={`calhead${date.done?' lit':''}`}>
      <span className="tile"><Icon name={date.done?'check_circle':'calendar_month'} filled/></span>
      <div>
        <p className="big">{date.label}</p>
        <p className="sub">{heading&&<b>{heading} </b>}{progress}</p>
      </div>
    </div>
    <MonthNav title={monthName} sub={year?`${reviewed.toLocaleString()} of ${plural(filed.length,'date')} reviewed`:undefined} onStep={step}/>
    <div ref={grid} className="calgrid" role="grid" aria-label={monthName} aria-busy={!year&&!failed} onKeyDown={walk}>
      {weekdays.map((day,index)=><span key={index} className="dow" aria-hidden="true">{day}</span>)}
      {Array.from({length:leadDays(layoutYear,month)},(_,index)=><span key={`lead${index}`}/>)}
      {days.map(({md,dom,cell})=><a key={md} href={`/on/${md}`} data-md={md} role="gridcell" tabIndex={md===focusable?0:-1}
        aria-current={md===date.md?'page':undefined} aria-label={describe(md,cell)}
        className={`d${cell?` ${cell.state}`:''}${md===today?' today':''}${md===date.md?' current':''}`}
        onFocus={()=>{if(focus!==md)setFocus(md)}}><span>{dom}</span>{(cell?.fresh??0)>0&&<i className="freshdot" aria-hidden="true"/>}</a>)}
    </div>
    {failed?<p className="calfoot" role="alert">How far each date has got did not load. <button type="button" className="textbtn" onClick={()=>setAttempt(value=>value+1)}>Try again</button></p>
      :<p className="calfoot callegend" aria-hidden="true"><span className="key done"/>Reviewed<span className="key part"/>Partly reviewed{days.some(day=>(day.cell?.fresh??0)>0)&&<><span className="freshdot"/>New files</>}</p>}
    <nav className="calsteps" aria-label="Nearby dates">
      <a href={`/on/${date.previous}`} rel="prev"><Icon name="arrow_back"/>{shortLabel(date.previous)}</a>
      {date.md!==today&&<a href={`/on/${today}`} className="totoday">Today</a>}
      <a href={`/on/${date.next}`} rel="next" className="fwd">{shortLabel(date.next)}<Icon name="arrow_back"/></a>
    </nav>
  </div>;
}
