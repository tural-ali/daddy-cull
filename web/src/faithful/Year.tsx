export type CalendarCell={md:string;dom:number;years:number;files:number;done:number;removed:number;state:'none'|'todo'|'part'|'done'};
export type CalendarMonth={name:string;cells:(CalendarCell|null)[]};
export type CalendarProgress={dates:number;done:number;part:number;filesDone:number;files:number};
export type YearData={months:CalendarMonth[];prog:CalendarProgress;today:string;streak:number;week:{days:number;seconds:number};refreshed?:string|null};

export function calendarLabel(md:string){
  const [month,day]=md.split('-').map(Number);
  return new Intl.DateTimeFormat('en-GB',{day:'numeric',month:'long',timeZone:'UTC'}).format(new Date(Date.UTC(2000,month-1,day)));
}
function minutes(seconds:number){return seconds<60?`${Math.round(seconds)}s`:`${Math.round(seconds/60)} min`}

/** JSX translation of templates/year.php with the same calendar and legend. */
function describe(cell:CalendarCell){
  const parts=[cell.years===0?'nothing waiting':`${cell.years} year${cell.years===1?'':'s'}, ${cell.files.toLocaleString()} files${cell.done>0?`, ${cell.done} reviewed this year`:''}`];
  if(cell.removed>0)parts.push(`${cell.removed.toLocaleString()} removed`);
  return `${calendarLabel(cell.md)} - ${parts.join(', ')}`;
}

export function Year({months,prog,streak,week,refreshed}:YearData){
  const dates=Math.max(1,prog.dates),percent=prog.done/dates*100;
  // Today is the viewer's own date, not the server's, so the outline moves at
  // the viewer's midnight.
  const now=new Date(),today=`${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`;
  return <section className="yearview">
    <h1>Your archive</h1>
    <p className="ysum"><b>{prog.done.toLocaleString()} of {dates.toLocaleString()}</b> calendar dates reviewed in {now.getFullYear()} <span className="pc">{percent.toFixed(1)}%</span>
      {prog.part>0&&<span className="dim"> · {prog.part} part-finished</span>}
      {streak>0&&<span className="dim"> · {streak} day streak</span>}
    </p>
    <div className="pbar wide"><span style={{width:`${percent}%`}}/></div>
    <div className="calendar">
      {months.map(month=><div className="cmonth" key={month.name}>
        <span className="mlabel">{month.name.slice(0,3)}</span>
        {month.cells.map((cell,index)=>cell===null?<span className="cell blank" key={index}/>:<a
          key={cell.md} className={`cell ${cell.state}${cell.md===today?' now':''}${cell.removed>0?' cut':''}`} href={`/on/${cell.md}`}
          title={describe(cell)} aria-label={describe(cell)}
        >{cell.dom}</a>)}
      </div>)}
    </div>
    <p className="legend">{[['none','nothing filed'],['todo','waiting'],['part','part-finished'],['done','reviewed this year'],['cut','files removed'],['now','today']].map(([state,label])=><span className="lg" key={state}><span className={`key ${state}`}/>{label}</span>)}</p>
    <p className="hint">{prog.filesDone.toLocaleString()} of {prog.files.toLocaleString()} files sit under a date you have finished this year. Every date comes back to waiting on 1 January.
      {week.days>0&&<> You reviewed on {week.days} of the last 7 days, {minutes(week.seconds)} in total.</>}
      {refreshed&&<> Index last refreshed {new Intl.DateTimeFormat('en-GB',{day:'numeric',month:'short',hour:'2-digit',minute:'2-digit'}).format(new Date(refreshed))}.</>}
    </p>
  </section>;
}
