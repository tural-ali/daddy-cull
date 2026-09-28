import {useCallback,useEffect,useRef,useState,type CSSProperties,type FormEvent,type KeyboardEvent as ReactKeyboardEvent,type ReactNode} from 'react';
import {Icon,type IconName} from '../Icon';
import {Logo,LogoMark} from '../Logo';
import {pathForDate} from './goto';
import {SelectionBar,SelectionProvider,type Selection} from './selection';
import {PageActionButtons,PageActionsProvider,type PageActions} from './pageActions';
import {StreakCalendar,StreakIntro,StreakPill,introDue} from './Streak';
import {NotificationBell} from './Notifications';
import {DateCalendar,DatePill,PageDateProvider,type PageDate} from './DatePicker';
import {GridZoom} from './gridZoom';
import {FilterButton,FilterMenu,FilterPills,PageFiltersProvider,matchFilters,type FilterOption,type PageFilters} from './SearchFilters';

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

/** The search field goes to a typed date. On a date's page that date sits in
 * the field as a pill, and the pill opens a calendar to pick another. A page
 * with filters puts them here too: each one that is on is a pill, the filter
 * button lists them all, and typing a filter's name offers it. */
function DateSearch({date,filters}:{date:PageDate|null;filters:PageFilters|null}){
  const [value,setValue]=useState('');
  const [problem,setProblem]=useState('');
  const [open,setOpen]=useState<'date'|'filters'|null>(null);
  const [active,setActive]=useState(0);
  const input=useRef<HTMLInputElement>(null);
  const pill=useRef<HTMLButtonElement>(null);
  const filterButton=useRef<HTMLButtonElement>(null);
  const close=useCallback(()=>setOpen(null),[]);
  const suggestions=filters?matchFilters(filters.options,value):[];
  function pick(option:FilterOption){
    filters?.toggle(option.id);
    setValue('');setActive(0);
    input.current?.focus();
  }
  function go(event:FormEvent){
    event.preventDefault();
    // On a phone the field is folded to its icon, so the icon opens it.
    if(!value.trim()){input.current?.focus();return}
    if(suggestions.length>0){pick(suggestions[Math.min(active,suggestions.length-1)]);return}
    const path=pathForDate(value);
    if(path)location.assign(path);
    else setProblem(filters?'Type a date, such as 14 Aug 2019, or a filter, such as videos.':'Type a date, such as 14 Aug 2019, or 14 Aug for every year.');
  }
  function keys(event:ReactKeyboardEvent<HTMLInputElement>){
    if(suggestions.length>0&&(event.key==='ArrowDown'||event.key==='ArrowUp')){
      event.preventDefault();
      const by=event.key==='ArrowDown'?1:-1;
      setActive(current=>(current+by+suggestions.length)%suggestions.length);
    }else if(event.key==='Escape'&&value){
      setValue('');setActive(0);
    }else if(event.key==='Backspace'&&!value&&filters){
      // As in a field of chips, Backspace in an empty field takes the last
      // filter off.
      const last=filters.options.filter(option=>option.on).at(-1);
      if(last){event.preventDefault();filters.toggle(last.id)}
    }
  }
  const listing=suggestions.length>0;
  const on=filters?.options.filter(option=>option.on).length??0;
  return <div className="searchwrap">
    <form className={`search${date?' dated':''}${on?' filtered':''}`} role="search" onSubmit={go}>
      <button type="submit" className="searchgo" aria-label={listing?'Apply the filter':'Go to the date'}
        onPointerDown={event=>{if(!value.trim())event.preventDefault()}}><Icon name="search"/></button>
      {date&&<DatePill date={date} pill={pill} open={open==='date'} onToggle={()=>setOpen(current=>current==='date'?null:'date')}/>}
      {filters&&<FilterPills filters={filters}/>}
      <input ref={input} type="search" value={value} role="combobox" aria-expanded={listing} aria-controls={listing?'search-suggestions':undefined}
        aria-activedescendant={listing?`search-suggestion-${Math.min(active,suggestions.length-1)}`:undefined} aria-autocomplete="list"
        aria-label={filters?'Filter, or go to a date':'Go to a date'} aria-invalid={problem?true:undefined} aria-describedby={problem?'search-problem':undefined}
        placeholder={filters?(on?'Add a filter or date':'Filter, or go to another date'):date?'Go to another date':'Go to a date, like 14 Aug 2019'} autoComplete="off" enterKeyHint="go"
        onChange={event=>{setValue(event.target.value);setProblem('');setActive(0)}} onKeyDown={keys}/>
      {filters&&<FilterButton filters={filters} button={filterButton} open={open==='filters'} onToggle={()=>setOpen(current=>current==='filters'?null:'filters')}/>}
      {problem&&<p id="search-problem" className="searchproblem" role="alert">{problem}</p>}
      {/* oxlint-disable-next-line jsx-a11y/no-noninteractive-element-to-interactive-role -- the combobox's listbox, see the options below */}
      {listing&&<ul id="search-suggestions" className="searchsuggest" role="listbox" aria-label="Filters">
        {/* oxlint-disable-next-line jsx-a11y/click-events-have-key-events, jsx-a11y/no-noninteractive-element-to-interactive-role -- a combobox: focus stays in the field, whose arrow keys and Enter drive these options */}
        {suggestions.map((option,index)=><li key={option.id} id={`search-suggestion-${index}`} role="option" aria-selected={index===Math.min(active,suggestions.length-1)}
          onPointerDown={event=>event.preventDefault()} onClick={()=>pick(option)} onPointerEnter={()=>setActive(index)}>
          <Icon name={option.icon} filled={option.on}/><span className="label">{option.on?'Stop showing only':'Show only'} <b>{option.label.toLowerCase()}</b></span><span className="n">{option.count.toLocaleString()}</span>
        </li>)}
      </ul>}
    </form>
    {open==='date'&&date&&<DateCalendar date={date} anchor={pill} onClose={close}/>}
    {open==='filters'&&filters&&<FilterMenu filters={filters} anchor={filterButton} onClose={close}/>}
  </div>;
}

/** The app's frame: a top bar with the logo, the review streak and date
 * search, a sidebar of destinations ending in Settings, and the page on a
 * raised panel beside it. Below tablet width the sidebar becomes a drawer
 * behind the menu button. */
const narrowQuery='(max-width: 1000px)';
const sideKey='cull-side';
function readSideHidden(){try{return localStorage.getItem(sideKey)==='hidden'}catch{return false}}

export function Layout({route,binFiles,reviewed,streak,notifications,onNotificationsRead,flash,children}:{route:LegacyRoute;binFiles:number;reviewed?:{done:number;total:number};streak?:{days:number;today:boolean};notifications?:number;onNotificationsRead?:()=>void;flash?:string;children:ReactNode}){
  const [drawer,setDrawer]=useState(false);
  const [selection,setSelection]=useState<Selection|null>(null);
  const [pageActions,setPageActions]=useState<PageActions|null>(null);
  const [pageDate,setPageDate]=useState<PageDate|null>(null);
  const [pageFilters,setPageFilters]=useState<PageFilters|null>(null);
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
  // The streak: its pill beside the logo, the calendar it opens, the opening
  // animation once a day, and a bump when today's first review lights it.
  const pill=useRef<HTMLButtonElement>(null);
  const [calendarOpen,setCalendarOpen]=useState(false);
  const closeCalendar=useCallback(()=>setCalendarOpen(false),[]);
  const [intro,setIntro]=useState(false);
  const [bump,setBump]=useState(false);
  const introChecked=useRef(false);
  const litBefore=useRef<boolean|null>(null);
  useEffect(()=>{
    if(!streak)return;
    if(!introChecked.current){introChecked.current=true;if(introDue(streak))setIntro(true)}
    if(litBefore.current===false&&streak.today)setBump(true);
    litBefore.current=streak.today;
  },[streak]);
  useEffect(()=>{if(!bump)return;const timer=setTimeout(()=>setBump(false),900);return()=>clearTimeout(timer)},[bump]);
  return <div className={`shell${drawer?' drawer-open':''}${sideHidden?' side-hidden':''}`}>
    {selection?<SelectionBar selection={selection}/>:<header className="gbar">
      <div className="gbarstart">
        <button type="button" className="iconbtn menu" aria-label={narrow?(drawer?'Close the menu':'Open the menu'):(sideHidden?'Expand the menu':'Collapse the menu')} title="Main menu" aria-expanded={menuOpen} aria-controls="side" onClick={toggleMenu}><Icon name={narrow&&drawer?'close':'menu'}/></button>
        <a className="brand" href="/" title="Today"><Logo/><LogoMark className="brandmark"/></a>
        {streak&&<div className="streakwrap">
          <StreakPill streak={streak} pill={pill} open={calendarOpen} bump={bump} onToggle={()=>setCalendarOpen(open=>!open)}/>
          {calendarOpen&&<StreakCalendar streak={streak} anchor={pill} onClose={closeCalendar}/>}
        </div>}
      </div>
      <DateSearch date={pageDate} filters={pageFilters}/>
      <div className="gbaracts">
        {pageActions&&<PageActionButtons page={pageActions}/>}
        {notifications!==undefined&&<NotificationBell unread={notifications} onRead={()=>onNotificationsRead?.()}/>}
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
        title={rail?`Reviewed ${reviewed.done.toLocaleString()} of ${reviewed.total.toLocaleString()} dates`:'Open the calendar'}>
        <span className="sideprogresshead"><Icon name="task_alt"/><span className="sidelabel">Reviewed</span></span>
        <span className="meter" role="progressbar" aria-label="Calendar dates reviewed" aria-valuemin={0} aria-valuemax={reviewed.total} aria-valuenow={reviewed.done}><span style={{width:`${Math.min(100,share*100)}%`}}/></span>
        <span className="sideprogressfoot"><span className="sideprogressnote">{reviewed.done.toLocaleString()} of {reviewed.total.toLocaleString()} dates</span></span>
      </a>}
      <nav className="sidefoot" aria-label="Settings">
        <a href="/settings" className={route==='settings'?'on':undefined} aria-current={route==='settings'?'page':undefined} title={rail?'Settings':undefined}>
          <Icon name="settings" filled={route==='settings'}/><span className="sidelabel">Settings</span>
        </a>
      </nav>
    </aside>
    <button type="button" className="scrim" tabIndex={-1} aria-hidden="true" onClick={()=>setDrawer(false)}/>
    <GridZoom enabled={gridRoutes.has(route)}/>
    {intro&&streak&&<StreakIntro streak={streak} target={pill} onDone={()=>{setIntro(false);setBump(true)}}/>}
    <div className="panel">
      {flash&&<p className="flash" role="status">{flash}</p>}
      <main className={`${gridRoutes.has(route)?'wide':''}${selection?' selecting':''}`||undefined}><SelectionProvider value={setSelection}><PageActionsProvider value={setPageActions}><PageDateProvider value={setPageDate}><PageFiltersProvider value={setPageFilters}>{children}</PageFiltersProvider></PageDateProvider></PageActionsProvider></SelectionProvider></main>
      <div className="snacks" id="snacks"/>
    </div>
  </div>;
}
