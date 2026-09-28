import {useEffect,useLayoutEffect,useRef,useState,type RefObject} from 'react';
import {Icon} from '../Icon';
import {Confetti,stillMotion,useCountUp} from './Celebration';
import {leadDays,MonthNav,usePopover,weekdays} from './CalendarPopover';
import {tipProps,useShortcut} from './keys';

export type StreakState={days:number;today:boolean};
type StreakCalendarData={streak:number;reviewedToday:boolean;best:number;days:string[]};

const zone=Intl.DateTimeFormat().resolvedOptions().timeZone;
const introKey='cull.streak-intro';
const introHold=1500;
const flightTime=720;

function localDay(date:Date){
  return `${date.getFullYear()}-${String(date.getMonth()+1).padStart(2,'0')}-${String(date.getDate()).padStart(2,'0')}`;
}
function unit(days:number){return days===1?'day':'days'}

/** Whether the opening animation is due: once a calendar day, for a streak
 * that exists, and never for a reviewer who has asked for less motion. The
 * day is claimed as it is read, so a second tab opened the same morning does
 * not play it again. */
export function introDue(streak:StreakState):boolean{
  if(streak.days<1||stillMotion())return false;
  const today=localDay(new Date());
  try{
    if(localStorage.getItem(introKey)===today)return false;
    localStorage.setItem(introKey,today);
  }catch{/* storage blocked: it plays on each load */}
  return true;
}

/** The streak in the header, beside the logo: a flame and the run of days,
 * lit once today has a review. It opens the calendar of the run. */
export function StreakPill({streak,pill,open,bump,onToggle}:{streak:StreakState;pill:RefObject<HTMLButtonElement|null>;open:boolean;bump:boolean;onToggle:()=>void}){
  const label=streak.today?`${streak.days} ${unit(streak.days)} in a row, today included`
    :streak.days>0?`${streak.days} ${unit(streak.days)} in a row. Review something today to keep it going`
    :'No streak yet. Review something today to start one';
  useShortcut('S',onToggle);
  return <button ref={pill} type="button" className={`streakpill${streak.today?' lit':''}${bump?' bump':''}`} aria-label={label} {...tipProps(label,'S')} aria-expanded={open} aria-haspopup="dialog" onClick={onToggle}>
    <Icon name="local_fire_department" filled={streak.today}/><b>{streak.days.toLocaleString()}</b>
  </button>;
}

type Cell={key:string;dom:number;on:boolean;today:boolean;future:boolean;joinLeft:boolean;joinRight:boolean};

/** One month, Monday first, each reviewed day a flame-coloured disc and each
 * run of days joined into one band along its week, as Duolingo draws it. */
function monthCells(year:number,month:number,days:Set<string>,today:string):(Cell|null)[]{
  const lead=leadDays(year,month);
  const count=new Date(year,month+1,0).getDate();
  const cells:(Cell|null)[]=Array.from({length:lead},()=>null);
  for(let dom=1;dom<=count;dom++){
    const date=new Date(year,month,dom);
    const key=localDay(date);
    const column=(lead+dom-1)%7;
    const on=days.has(key);
    cells.push({key,dom,on,today:key===today,future:key>today,
      joinLeft:on&&column>0&&dom>1&&days.has(localDay(new Date(year,month,dom-1))),
      joinRight:on&&column<6&&dom<count&&days.has(localDay(new Date(year,month,dom+1)))});
  }
  return cells;
}

/** The calendar behind the streak: the current run and the best one, and a
 * month at a time of the days that had a review. */
export function StreakCalendar({streak,onClose,anchor}:{streak:StreakState;onClose:()=>void;anchor:RefObject<HTMLButtonElement|null>}){
  const [data,setData]=useState<StreakCalendarData|null>(null);
  const [failed,setFailed]=useState(false);
  const [attempt,setAttempt]=useState(0);
  const now=new Date();
  const [shown,setShown]=useState({year:now.getFullYear(),month:now.getMonth()});
  const panel=useRef<HTMLDivElement>(null);
  const keys=usePopover(panel,anchor,onClose);
  useEffect(()=>{
    const controller=new AbortController();
    setFailed(false);
    fetch(`/api/streak?tz=${encodeURIComponent(zone)}`,{signal:controller.signal}).then(response=>{
      if(!response.ok)throw new Error();
      return response.json() as Promise<StreakCalendarData>;
    }).then(setData).catch(()=>{if(!controller.signal.aborted)setFailed(true)});
    return()=>controller.abort();
  },[attempt]);
  useEffect(()=>{panel.current?.focus()},[]);
  const days=new Set(data?.days??[]);
  const today=localDay(now);
  const earliest=data?.days[0];
  const firstMonth=earliest?{year:Number(earliest.slice(0,4)),month:Number(earliest.slice(5,7))-1}:{year:now.getFullYear(),month:now.getMonth()};
  const atStart=shown.year*12+shown.month<=firstMonth.year*12+firstMonth.month;
  const atEnd=shown.year===now.getFullYear()&&shown.month===now.getMonth();
  const step=(by:number)=>setShown(current=>{const index=current.year*12+current.month+by;return {year:Math.floor(index/12),month:index%12}});
  const cells=monthCells(shown.year,shown.month,days,today);
  const monthName=new Date(shown.year,shown.month,1).toLocaleDateString('en-GB',{month:'long',year:'numeric'});
  const reviewedThisMonth=cells.filter(cell=>cell?.on).length;
  const current=data?.streak??streak.days;
  const lit=data?data.reviewedToday:streak.today;
  return <div ref={panel} className="calpop streakcal" role="dialog" aria-label="Review streak" tabIndex={-1} {...keys}>
    <div className={`calhead${lit?' lit':''}`}>
      <span className="tile"><Icon name="local_fire_department" filled/></span>
      <div>
        <p className="big"><b>{current.toLocaleString()}</b> {unit(current)} in a row</p>
        <p className="sub">{lit?'Today counts. Come back tomorrow to keep it going.':current>0?`Review a day today to make it ${current+1}.`:'Review a day today to start a streak.'}</p>
      </div>
    </div>
    <MonthNav title={monthName} sub={data?`${reviewedThisMonth} ${unit(reviewedThisMonth)} reviewed`:undefined} atStart={atStart} atEnd={atEnd} onStep={step}/>
    <div className="calgrid" role="grid" aria-label={monthName} aria-busy={!data&&!failed}>
      {weekdays.map((day,index)=><span key={index} className="dow" aria-hidden="true">{day}</span>)}
      {cells.map((cell,index)=>cell===null?<span key={`lead${index}`}/>:
        <span key={cell.key} role="gridcell" aria-label={`${cell.dom}${cell.on?', reviewed':''}${cell.today?', today':''}`}
          className={`d${cell.on?' on':''}${cell.joinLeft?' jl':''}${cell.joinRight?' jr':''}${cell.today?' today':''}${cell.future?' future':''}`}><span>{cell.dom}</span></span>)}
    </div>
    <p className="calfoot" role={failed?'alert':undefined}>{failed?<>The review days did not load. <button type="button" className="textbtn" onClick={()=>setAttempt(value=>value+1)}>Try again</button></>
      :data?<>Longest streak <b>{data.best.toLocaleString()} {unit(data.best)}</b></>:'Loading…'}</p>
  </div>;
}

/** The opening moment: the streak fills the screen, the count climbs, paper
 * falls, and then the flame and number fly into the pill in the header. A
 * click or key skips straight to the flight. */
export function StreakIntro({streak,target,onDone}:{streak:StreakState;target:RefObject<HTMLButtonElement|null>;onDone:()=>void}){
  const [phase,setPhase]=useState<'count'|'party'|'fly'>('count');
  const count=useCountUp(streak.days,true);
  const hero=useRef<HTMLDivElement>(null);
  const scrim=useRef<HTMLDivElement>(null);
  const done=useRef(onDone);
  done.current=onDone;
  useEffect(()=>{
    if(phase!=='count')return;
    const timer=setTimeout(()=>setPhase('party'),1150);
    return()=>clearTimeout(timer);
  },[phase]);
  useEffect(()=>{
    if(phase!=='party')return;
    const timer=setTimeout(()=>setPhase('fly'),introHold);
    return()=>clearTimeout(timer);
  },[phase]);
  useEffect(()=>{
    if(phase==='fly')return;
    const skip=()=>setPhase('fly');
    addEventListener('keydown',skip);
    return()=>removeEventListener('keydown',skip);
  },[phase]);
  useLayoutEffect(()=>{
    if(phase!=='fly')return;
    const from=hero.current?.querySelector('.introflame')?.getBoundingClientRect();
    const to=target.current?.getBoundingClientRect();
    const ease='cubic-bezier(.2, 0, 0, 1)';
    scrim.current?.animate([{opacity:1},{opacity:0}],{duration:flightTime*.8,easing:ease,fill:'forwards'});
    if(!from||!to||to.width===0){
      const fade=hero.current?.animate([{opacity:1},{opacity:0}],{duration:300,fill:'forwards'});
      fade?.finished.then(()=>done.current(),()=>done.current());
      if(!fade)done.current();
      return;
    }
    // The flame lands on the pill's flame: the whole block moves by the gap
    // between their centres and shrinks by the ratio of their sizes.
    const scale=to.height*.62/from.height;
    const dx=(to.left+to.height*.5)-(from.left+from.width/2);
    const dy=(to.top+to.height/2)-(from.top+from.height/2);
    hero.current?.querySelectorAll('.introtext').forEach(element=>element.animate([{opacity:1},{opacity:0}],{duration:flightTime*.35,easing:ease,fill:'forwards'}));
    // The number hands over to the pill's own as the flame arrives.
    hero.current?.querySelector('.introcount')?.animate([{opacity:1},{opacity:1,offset:.45},{opacity:0}],{duration:flightTime,easing:ease,fill:'forwards'});
    const flight=hero.current?.animate([
      {transform:'translate(0, 0) scale(1)'},
      {transform:`translate(${dx*.55}px, ${dy*.35}px) scale(${Math.max(scale,.5)})`,offset:.45},
      {transform:`translate(${dx}px, ${dy}px) scale(${scale})`},
    ],{duration:flightTime,easing:ease,fill:'forwards'});
    flight?.finished.then(()=>done.current(),()=>done.current());
  },[phase,target]);
  // oxlint-disable-next-line jsx-a11y/click-events-have-key-events, jsx-a11y/no-noninteractive-element-interactions -- a click skips the intro, as any key does
  return <div className="streakintro" role="dialog" aria-modal="true" aria-label={`${streak.days} ${unit(streak.days)} in a row`} onClick={()=>setPhase('fly')}>
    <div ref={scrim} className="introscrim"/>
    {phase==='party'&&<Confetti/>}
    <div ref={hero} className={`introhero${phase!=='count'?' landed':''}`}>
      <span className={`introflame${streak.today?' lit':''}`}><Icon name="local_fire_department" filled/></span>
      <p className="introcount"><b>{count.toLocaleString()}</b></p>
      <p className="introtext introunit">{unit(streak.days)} in a row</p>
      <p className="introtext introsub">{streak.today?'Today already counts. Lovely work.':`Review a day today to make it ${streak.days+1}.`}</p>
    </div>
  </div>;
}
