import {useCallback,useEffect,useRef,useState,type RefObject} from 'react';
import {Icon} from '../Icon';
import {usePopover} from './CalendarPopover';
import {CATALOGUE_CHANGED} from './catalogueWatch';
import {tipProps,useShortcut} from './keys';

// What reached the catalogue with nobody on a page to see it: files from
// iCloud landing on their dates at night, and photos deleted on a phone being
// marked for the Bin. The bell counts what has not been seen; opening it
// lists them and marks them seen.

type NotificationDay={day:string;md:string;files:number;reopened:boolean;fresh:number};
type Notification={id:number;kind:'arrivals'|'phone-deletions';createdAt:string;files:number;bytes:number;read:boolean;days:NotificationDay[]};
type NotificationList={unread:number;items:Notification[]};

/** How many dates an arrival lists before summing up the rest. */
const shownDays=6;

function plural(count:number,word:string){return `${count.toLocaleString()} ${word}${count===1?'':'s'}`}
function size(bytes:number){
  if(bytes<1e6)return `${Math.max(1,Math.round(bytes/1e3))} KB`;
  if(bytes<1e9)return `${(bytes/1e6).toFixed(bytes<1e7?1:0)} MB`;
  return `${(bytes/1e9).toFixed(1)} GB`;
}
// The server keeps UTC as "2026-09-28 04:43:10.000"; the reader sees their own
// clock, and "today" and "yesterday" by their own calendar.
function when(stamp:string,now=new Date()){
  const at=new Date(stamp.replace(' ','T')+(/[zZ]|[+-]\d\d:\d\d$/.test(stamp)?'':'Z'));
  if(Number.isNaN(at.getTime()))return stamp;
  const time=at.toLocaleTimeString('en-GB',{hour:'2-digit',minute:'2-digit'});
  const days=Math.round((new Date(now.getFullYear(),now.getMonth(),now.getDate()).getTime()-new Date(at.getFullYear(),at.getMonth(),at.getDate()).getTime())/864e5);
  if(days===0)return `Today, ${time}`;
  if(days===1)return `Yesterday, ${time}`;
  return `${at.toLocaleDateString('en-GB',{day:'numeric',month:'short',...(at.getFullYear()!==now.getFullYear()?{year:'numeric'}:{})})}, ${time}`;
}
function dayLabel(day:string){
  return new Intl.DateTimeFormat('en-GB',{day:'numeric',month:'short',year:'numeric',timeZone:'UTC'}).format(new Date(`${day}T00:00:00Z`));
}

/** The bell in the top bar, its count of what has not been seen, and the
 * list it opens. */
export function NotificationBell({unread,onRead}:{unread:number;onRead:()=>void}){
  const [open,setOpen]=useState(false);
  const button=useRef<HTMLButtonElement>(null);
  const close=useCallback(()=>setOpen(false),[]);
  const label=unread>0?`Notifications, ${unread.toLocaleString()} new`:'Notifications';
  useShortcut('N',()=>setOpen(current=>!current));
  return <div className="bellwrap">
    <button ref={button} type="button" className={`iconbtn bell${open?' on':''}`} aria-label={label} {...tipProps('Notifications','N')} aria-expanded={open} aria-haspopup="dialog" onClick={()=>setOpen(current=>!current)}>
      <Icon name="notifications" filled={open}/>
      {unread>0&&<span className="bellcount" aria-hidden="true">{unread>9?'9+':unread}</span>}
    </button>
    {open&&<NotificationPanel anchor={button} onClose={close} onRead={onRead}/>}
  </div>;
}

function NotificationPanel({anchor,onClose,onRead}:{anchor:RefObject<HTMLButtonElement|null>;onClose:()=>void;onRead:()=>void}){
  const [list,setList]=useState<NotificationList|null>(null);
  const [failed,setFailed]=useState(false);
  const [attempt,setAttempt]=useState(0);
  // What was new when the list opened stays marked as new while it is open,
  // although it is marked seen straight away.
  const [fresh,setFresh]=useState<Set<number>>(new Set());
  const panel=useRef<HTMLDivElement>(null);
  const keys=usePopover(panel,anchor,onClose);
  const read=useRef(onRead);
  read.current=onRead;
  useEffect(()=>{panel.current?.focus()},[]);
  useEffect(()=>{
    const reread=()=>setAttempt(value=>value+1);
    window.addEventListener(CATALOGUE_CHANGED,reread);
    return()=>window.removeEventListener(CATALOGUE_CHANGED,reread);
  },[]);
  useEffect(()=>{
    const controller=new AbortController();
    setFailed(false);
    fetch('/api/notifications',{signal:controller.signal,cache:'no-store'}).then(response=>{
      if(!response.ok)throw new Error();
      return response.json() as Promise<NotificationList>;
    }).then(next=>{
      setList(next);
      const unseen=next.items.filter(item=>!item.read);
      if(unseen.length===0)return;
      setFresh(current=>new Set([...current,...unseen.map(item=>item.id)]));
      // Seen means seen up to the newest listed; one arriving meanwhile stays new.
      void fetch('/api/notifications/read',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({through:next.items[0].id})})
        .then(response=>{if(response.ok)read.current()}).catch(()=>{});
    }).catch(()=>{if(!controller.signal.aborted)setFailed(true)});
    return()=>controller.abort();
  },[attempt]);
  return <div ref={panel} className="calpop notepop" role="dialog" aria-label="Notifications" tabIndex={-1} {...keys}>
    <div className="notehead"><h2>Notifications</h2></div>
    {failed?<p className="notenote" role="alert">Notifications did not load. <button type="button" className="textbtn" onClick={()=>setAttempt(value=>value+1)}>Try again</button></p>
      :list===null?<p className="notenote" aria-busy="true">Reading notifications…</p>
      :list.items.length===0?<p className="notenote">Nothing yet. New files from iCloud and photos deleted on a phone show here.</p>
      :<ul className="notelist">{list.items.map(item=><NotificationItem key={item.id} item={item} unseen={fresh.has(item.id)}/>)}</ul>}
  </div>;
}

function NotificationItem({item,unseen}:{item:Notification;unseen:boolean}){
  const arrivals=item.kind==='arrivals';
  const reopened=item.days.filter(day=>day.reopened).length;
  const rest=item.days.length-shownDays;
  return <li className={`noteitem${unseen?' unseen':''}`}>
    <span className={`notetile ${arrivals?'arrivals':'deletions'}`}><Icon name={arrivals?'photo':'delete'}/></span>
    <div className="notebody">
      <p className="notetitle">{arrivals
        ?<>{plural(item.files,'new file')} reached the archive</>
        :<>{plural(item.files,'photo')} deleted on a phone {item.files===1?'was':'were'} marked for the Bin</>}</p>
      <p className="notemeta">
        <span>{when(item.createdAt)}</span>
        {item.bytes>0&&<span>{size(item.bytes)}</span>}
        {arrivals&&item.days.length>0&&<span>{plural(item.days.length,'day')}{reopened>0&&`, ${reopened.toLocaleString()} reopened`}</span>}
      </p>
      {arrivals&&item.days.length>0&&<ul className="notedays" aria-label="Dates">
        {item.days.slice(0,shownDays).map(day=><li key={day.day}>
          <a className="noteday" href={`/on/${day.md}`} title={day.fresh>0?`${plural(day.fresh,'file')} still waiting${day.reopened?'; the date had been reviewed':''}`:day.reopened?'Reopened, and reviewed again since':'Reviewed since'}>
            {day.fresh>0&&<span className="freshdot" aria-hidden="true"/>}
            <span>{dayLabel(day.day)}</span><span className="n">{day.files.toLocaleString()}</span>
            {day.fresh>0&&<span className="vh">, {plural(day.fresh,'file')} still waiting</span>}
          </a>
        </li>)}
        {rest>0&&<li className="notemore">and {plural(rest,'more date')}</li>}
      </ul>}
      {!arrivals&&<p className="noteacts"><a href="/bin">Open the Bin</a></p>}
    </div>
  </li>;
}
