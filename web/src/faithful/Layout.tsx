import {useEffect,useRef,useState,type CSSProperties,type FormEvent,type ReactNode} from 'react';
import {Icon,type IconName} from '../Icon';
import {Logo} from '../Logo';
import {pathForDate} from './goto';
import {SelectionBar,SelectionProvider,type Selection} from './selection';
import {PageActionButtons,PageActionsProvider,type PageActions} from './pageActions';

export type LegacyRoute='today'|'year'|'dupes'|'upgrades'|'shadows'|'shots'|'social'|'photos'|'log'|'bin'|'settings';
type Item={href:string;route:LegacyRoute;label:string;icon:IconName};

// The sidebar groups destinations by what they are for, the way Google Photos
// separates its library from its collections: reviewing the archive, clearing
// what should not be in it, keeping Apple Photos in step, and the record.
const sections:{title?:string;items:Item[]}[]=[
  {items:[
    {href:'/',route:'today',label:'Today',icon:'photo'},
    {href:'/year',route:'year',label:'Year',icon:'calendar_month'},
  ]},
  {title:'Clean up',items:[
    {href:'/duplicates',route:'dupes',label:'Duplicates',icon:'filter_none'},
    {href:'/shadows',route:'shadows',label:'Shadowed',icon:'layers'},
    {href:'/screenshots',route:'shots',label:'Screenshots',icon:'screenshot_region'},
    {href:'/social',route:'social',label:'Saved from social',icon:'forum'},
  ]},
  {title:'Sync',items:[
    {href:'/photos',route:'photos',label:'Apple Photos',icon:'cloud_sync'},
  ]},
  {title:'History',items:[
    {href:'/log',route:'log',label:'Log',icon:'history'},
    {href:'/bin',route:'bin',label:'Bin',icon:'delete'},
  ]},
];

// Pages built around a grid run the full width of the panel, as in Google
// Photos; the Year calendar, Settings and Apple Photos keep a reading measure,
// since a calendar stretched across a wide screen is hard to read along a row.
const gridRoutes=new Set<LegacyRoute>(['today','dupes','upgrades','shadows','shots','social','log','bin']);

function DateSearch(){
  const [value,setValue]=useState('');
  const [problem,setProblem]=useState('');
  const input=useRef<HTMLInputElement>(null);
  function go(event:FormEvent){
    event.preventDefault();
    // On a phone the field is folded to its icon, so the icon opens it.
    if(!value.trim()){input.current?.focus();return}
    const path=pathForDate(value);
    if(path)location.assign(path);
    else setProblem('Type a date, such as 14 Aug 2019, or 14 Aug for every year.');
  }
  return <form className="search" role="search" onSubmit={go}>
    <button type="submit" className="searchgo" aria-label="Go to the date"
      onPointerDown={event=>{if(!value.trim())event.preventDefault()}}><Icon name="search"/></button>
    <input ref={input} type="search" value={value} aria-label="Go to a date" aria-invalid={problem?true:undefined} aria-describedby={problem?'search-problem':undefined}
      placeholder="Go to a date, like 14 Aug 2019" autoComplete="off" enterKeyHint="go"
      onChange={event=>{setValue(event.target.value);setProblem('')}}/>
    {problem&&<p id="search-problem" className="searchproblem" role="alert">{problem}</p>}
  </form>;
}

/** The app's frame: a top bar with the logo, date search and Settings, a
 * sidebar of destinations, and the page on a raised panel beside it. Below
 * tablet width the sidebar becomes a drawer behind the menu button. */
const narrowQuery='(max-width: 1000px)';
const sideKey='cull-side';
function readSideHidden(){try{return localStorage.getItem(sideKey)==='hidden'}catch{return false}}

/** The run of days in a row with a review, kept in view to keep it going. It
 * is lit once today counts, and asks for today's review until then. */
function Streak({days,today}:{days:number;today:boolean}){
  const unit=days===1?'day':'days';
  const title=today?`${days} ${unit} in a row with a review, today included`
    :days>0?`${days} ${unit} in a row. Review something today to keep it going`
    :'Review something today to start a streak';
  // It sits inside the Reviewed link, which already opens the calendar.
  return <span className={`streak${today?' lit':''}${days>0?'':' none'}`} title={title} aria-label={title}>
    <Icon name="local_fire_department" filled={today}/>{days>0?<span><b>{days.toLocaleString()}</b><span className="unit"> {unit}</span></span>:<span className="unit">Start a streak</span>}
  </span>;
}

export function Layout({route,binFiles,reviewed,streak,flash,children}:{route:LegacyRoute;binFiles:number;reviewed?:{done:number;total:number};streak?:{days:number;today:boolean};flash?:string;children:ReactNode}){
  const [drawer,setDrawer]=useState(false);
  const [selection,setSelection]=useState<Selection|null>(null);
  const [pageActions,setPageActions]=useState<PageActions|null>(null);
  // On a wide screen the menu button hides the sidebar, as in Google Photos,
  // and the choice is remembered; on a narrow one it opens the drawer.
  const [sideHidden,setSideHidden]=useState(readSideHidden);
  const [narrow,setNarrow]=useState(()=>matchMedia(narrowQuery).matches);
  useEffect(()=>{
    const query=matchMedia(narrowQuery);
    const change=()=>{setNarrow(query.matches);setDrawer(false)};
    query.addEventListener('change',change);
    return()=>query.removeEventListener('change',change);
  },[]);
  function toggleMenu(){
    if(narrow){setDrawer(open=>!open);return}
    setSideHidden(hidden=>{
      try{if(hidden)localStorage.removeItem(sideKey);else localStorage.setItem(sideKey,'hidden')}catch{/* remembered for this page only */}
      return !hidden;
    });
  }
  const menuOpen=narrow?drawer:!sideHidden;
  // Folded on a wide screen, the sidebar is a rail of icons whose labels
  // become tooltips.
  const rail=!narrow&&sideHidden;
  useEffect(()=>{
    if(!drawer)return;
    const close=(event:KeyboardEvent)=>{if(event.key==='Escape')setDrawer(false)};
    window.addEventListener('keydown',close);
    return()=>window.removeEventListener('keydown',close);
  },[drawer]);
  const share=reviewed&&reviewed.total>0?reviewed.done/reviewed.total:0;
  return <div className={`shell${drawer?' drawer-open':''}${sideHidden?' side-hidden':''}`}>
    {selection?<SelectionBar selection={selection}/>:<header className="gbar">
      <button type="button" className="iconbtn menu" aria-label={narrow?(drawer?'Close the menu':'Open the menu'):(sideHidden?'Expand the menu':'Collapse the menu')} title="Main menu" aria-expanded={menuOpen} aria-controls="side" onClick={toggleMenu}><Icon name={narrow&&drawer?'close':'menu'}/></button>
      <a className="brand" href="/" title="Today"><Logo/></a>
      <DateSearch/>
      <div className="gbaracts">
        {pageActions&&<PageActionButtons page={pageActions}/>}
        <a className={`iconbtn${route==='settings'?' on':''}`} href="/settings" aria-label="Settings" title="Settings" aria-current={route==='settings'?'page':undefined}><Icon name="settings" filled={route==='settings'}/></a>
      </div>
    </header>}
    <aside id="side" className="side">
      <nav aria-label="Main navigation">
        {sections.map((section,index)=><div key={index} className="sidesec">
          {section.title&&<h2 className="sidetitle">{section.title}</h2>}
          {section.items.map(item=>{
            const on=route===item.route;
            return <a key={item.route} href={item.href} className={on?'on':undefined} aria-current={on?'page':undefined} title={rail?item.label:undefined}>
              <Icon name={item.icon} filled={on}/><span className="sidelabel">{item.label}</span>
              {item.route==='bin'&&binFiles>0&&<span className="count" aria-label={`${binFiles.toLocaleString()} file${binFiles===1?'':'s'}`}>{binFiles.toLocaleString()}</span>}
            </a>;
          })}
        </div>)}
      </nav>
      {reviewed&&reviewed.total>0&&<a className="sideprogress" href="/year" style={{'--share':Math.min(1,share)} as CSSProperties}
        title={rail?`Reviewed ${reviewed.done.toLocaleString()} of ${reviewed.total.toLocaleString()} dates${streak&&streak.days>0?` · ${streak.days.toLocaleString()} ${streak.days===1?'day':'days'} in a row`:''}`:'Open the calendar'}>
        <span className="sideprogresshead"><Icon name="task_alt"/><span className="sidelabel">Reviewed</span></span>
        <span className="meter" role="progressbar" aria-label="Calendar dates reviewed" aria-valuemin={0} aria-valuemax={reviewed.total} aria-valuenow={reviewed.done}><span style={{width:`${Math.min(100,share*100)}%`}}/></span>
        <span className="sideprogressfoot"><span className="sideprogressnote">{reviewed.done.toLocaleString()} of {reviewed.total.toLocaleString()} dates</span>{streak&&<Streak days={streak.days} today={streak.today}/>}</span>
      </a>}
    </aside>
    <button type="button" className="scrim" tabIndex={-1} aria-hidden="true" onClick={()=>setDrawer(false)}/>
    <div className="panel">
      {flash&&<p className="flash" role="status">{flash}</p>}
      <main className={`${gridRoutes.has(route)?'wide':''}${selection?' selecting':''}`||undefined}><SelectionProvider value={setSelection}><PageActionsProvider value={setPageActions}>{children}</PageActionsProvider></SelectionProvider></main>
    </div>
  </div>;
}
