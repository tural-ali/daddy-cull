import type {CSSProperties} from 'react';

// fresh counts files that reached the archive after the date was reviewed and
// still wait: the date's red dot.
export type CalendarCell={md:string;dom:number;years:number;files:number;done:number;waiting:number;state:'none'|'todo'|'part'|'done';fresh?:number};
export type CalendarMonth={name:string;cells:(CalendarCell|null)[]};
export type CalendarProgress={dates:number;done:number;part:number;filesDone:number;files:number};
export type YearData={months:CalendarMonth[];prog:CalendarProgress;today:string;streak:number;week:{days:number;seconds:number};refreshed?:string|null};

export function calendarLabel(md:string){
  const [month,day]=md.split('-').map(Number);
  return new Intl.DateTimeFormat('en-GB',{day:'numeric',month:'long',timeZone:'UTC'}).format(new Date(Date.UTC(2000,month-1,day)));
}
function minutes(seconds:number){return seconds<60?`${Math.round(seconds)}s`:`${Math.round(seconds/60)} min`}

/** JSX translation of templates/year.php with the same calendar and legend. */
/** How much still waits on a date: fewer than 30 files, 30 to 100, or more. */
function heat(cell:CalendarCell){
  if(cell.state==='none'||cell.state==='done')return cell.state;
  return cell.waiting>100?'high':cell.waiting>=30?'mid':'low';
}
/** Where a date sits within its band, 0 for the fewest waiting and 1 for the
 * most, so a busier date draws in a deeper shade of its colour. The top band
 * has no ceiling, so it runs on a log scale up to the busiest date: one date
 * with a thousand files must not wash every other red date out to pale. */
function depth(cell:CalendarCell,busiest:number){
  const band=heat(cell);
  if(band==='low')return Math.min(1,Math.max(0,(cell.waiting-1)/28));
  if(band==='mid')return Math.min(1,Math.max(0,(cell.waiting-30)/70));
  if(band==='high')return busiest>100?Math.min(1,Math.log(cell.waiting/100)/Math.log(busiest/100)):1;
  return 0;
}
function plural(count:number,word:string){return `${count.toLocaleString()} ${word}${count===1?'':'s'}`}
/** How a date's new arrivals read after its description. */
export function freshNote(cell:{fresh?:number}){return (cell.fresh??0)>0?`, ${plural(cell.fresh!,'newly arrived file')}`:''}
function describe(cell:CalendarCell){
  if(cell.years===0)return `${calendarLabel(cell.md)} - nothing in the archive`;
  if(cell.state==='done')return `${calendarLabel(cell.md)} - reviewed, ${plural(cell.files,'file')} across ${plural(cell.years,'year')}`;
  return `${calendarLabel(cell.md)} - ${plural(cell.waiting,'file')} waiting${cell.done>0?`, ${plural(cell.done,'year')} already reviewed`:''}${freshNote(cell)}`;
}

export function Year({months,prog,streak,week,refreshed}:YearData){
  const dates=Math.max(1,prog.dates),percent=prog.done/dates*100;
  // Today is the viewer's own date, not the server's, so the outline moves at
  // the viewer's midnight.
  const busiest=months.reduce((most,month)=>month.cells.reduce((inner,cell)=>cell&&cell.state!=='done'?Math.max(inner,cell.waiting):inner,most),0);
  const now=new Date(),today=`${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`;
  return <section className="yearview">
    <h1>Your archive</h1>
    <p className="ysum"><b>{prog.done.toLocaleString()} of {dates.toLocaleString()}</b> calendar dates reviewed <span className="pc">{percent.toFixed(1)}%</span>
      {prog.part>0&&<span className="dim"> · {prog.part} part-finished</span>}
      {streak>0&&<span className="dim"> · {streak} day streak</span>}
    </p>
    <div className="pbar wide"><span style={{width:`${percent}%`}}/></div>
    <div className="calendar">
      {months.map(month=><div className="cmonth" key={month.name}>
        <span className="mlabel">{month.name.slice(0,3)}</span>
        {month.cells.map((cell,index)=>cell===null?<span className="cell blank" key={index}/>:<a
          key={cell.md} className={`cell ${heat(cell)}${cell.md===today?' now':''}`} href={`/on/${cell.md}`}
          style={{'--depth':depth(cell,busiest).toFixed(3)} as CSSProperties}
          title={describe(cell)} aria-label={describe(cell)}
        >{cell.dom}{(cell.fresh??0)>0&&<span className="freshdot" aria-hidden="true"/>}</a>)}
      </div>)}
    </div>
    <div className="legend">
      <p className="lgrow">
        <span className="lgkey low">Fewer than 30</span>
        <span className="lgkey mid">30 to 100</span>
        <span className="lgkey high">More than 100</span>
        <span className="lgnote">Files still waiting on each date.{busiest>0&&<> Deeper shades have more, up to {busiest.toLocaleString()} on the busiest.</>}</span>
      </p>
      <p className="lgrow">
        <span className="lgkey done">Reviewed</span>
        <span className="lgkey none">Nothing filed</span>
        <span className="lgkey now">Today</span>
        <span className="lgkey fresh">New files<span className="freshdot"/></span>
      </p>
    </div>
    <p className="hint">{prog.filesDone.toLocaleString()} of {prog.files.toLocaleString()} files sit under a date you have finished. A reviewed date comes back, with a red dot, when new files reach it.
      {week.days>0&&<> You reviewed on {week.days} of the last 7 days, {minutes(week.seconds)} in total.</>}
      {refreshed&&<> Index last refreshed {new Intl.DateTimeFormat('en-GB',{day:'numeric',month:'short',hour:'2-digit',minute:'2-digit'}).format(new Date(refreshed))}.</>}
    </p>
  </section>;
}
