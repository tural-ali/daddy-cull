import {ProgressBar} from '../ProgressBar';
import {useCallback,useEffect,useMemo,useRef,useState,type CSSProperties,type FormEvent,type KeyboardEvent as ReactKeyboardEvent,type ReactNode} from 'react';
import {Icon,addonIcon,type IconName} from '../Icon';
import {useAddons,type Addon,type AddonSection} from './addonList';
import {Logo,LogoMark} from '../Logo';
import {pathForDate} from './goto';
import {navigate} from './router';
import {SelectionBar,SelectionProvider,type Selection} from './selection';
import {PageActionButtons,PageActionsProvider,type PageActions} from './pageActions';
import {StreakCalendar,StreakIntro,StreakPill,introDue} from './Streak';
import {NotificationBell} from './Notifications';
import {TasksButton} from './Tasks';
import {DateCalendar,DatePill,PageDateProvider,type PageDate} from './DatePicker';
import {GridZoom} from './gridZoom';
import {FilterButton,FilterMenu,FilterPills,PageFiltersProvider,matchFilters,unusualSort,type PageFilters,type Suggestion} from './SearchFilters';
import {Kbd,Tips,tipProps,useShortcut} from './keys';
import {GuideButton} from './PageGuide';
import {SideLibrary} from './SideLibrary';
import type {LibraryTotals} from './Settings';

export type LegacyRoute='today'|'year'|'dupes'|'upgrades'|'shadows'|'shots'|'social'|'photos'|'google'|'log'|'bin'|'settings'|'addons'|'developers'|'setup'|'frame';
type Item={href:string;route?:LegacyRoute;label:string;icon:IconName};
type Section={title?:string;items:Item[]};

// Cull's own addons' pages, where the app draws them, in the order the
// sidebar lists them.
const addonRoutes:Record<string,LegacyRoute>={'/shadows':'shadows','/screenshots':'shots','/social':'social','/upgrades':'upgrades','/photos':'photos','/google-photos':'google'};
const addonOrder=Object.keys(addonRoutes);
// Before the addons are read, or if they cannot be, the sidebar offers what
// it always has.
const usualPages:Record<AddonSection,Item[]>={
  collections:[
    {href:'/shadows',route:'shadows',label:'Shadowed',icon:'layers'},
    {href:'/screenshots',route:'shots',label:'Screenshots',icon:'screenshot_region'},
    {href:'/social',route:'social',label:'Saved from social',icon:'forum'},
  ],
  sync:[{href:'/photos',route:'photos',label:'Apple Photos',icon:'cloud_sync'}],
  tools:[],
};

/** Each section's pages from the addons that are on: Cull's own in their
 * usual order, then yours by name. */
function addonPages(addons:Addon[]|null):Record<AddonSection,Item[]>{
  if(!addons)return usualPages;
  const pages={collections:[],sync:[],tools:[]} as Record<AddonSection,Item[]>;
  const rank=(path:string)=>{const index=addonOrder.indexOf(path);return index<0?addonOrder.length:index};
  const on=addons.filter(addon=>addon.on).flatMap(addon=>(addon.pages??[]).map(page=>({addon,page})));
  on.sort((a,b)=>Number(!a.addon.builtIn)-Number(!b.addon.builtIn)||rank(a.page.path)-rank(b.page.path));
  for(const {page} of on)pages[page.section]?.push({href:page.path,route:addonRoutes[page.path],label:page.label,icon:addonIcon(page.icon)});
  return pages;
}

// The sidebar groups destinations by what they are for, the way Google Photos
// separates its library from its collections: reviewing the archive, clearing
// what should not be in it, keeping other apps in step, the addons' own
// tools, and the record. A section with nothing in it is left out.
function sidebar(addons:Addon[]|null):Section[]{
  const pages=addonPages(addons);
  const sections:Section[]=[
    {items:[
      {href:'/today',route:'today',label:'Today',icon:'photo'},
      {href:'/year',route:'year',label:'Year',icon:'calendar_month'},
    ]},
    {title:'Collections',items:[{href:'/duplicates',route:'dupes',label:'Duplicates',icon:'filter_none'},...pages.collections]},
    {title:'Sync',items:pages.sync},
    {title:'Tools',items:pages.tools},
    {title:'History',items:[
      {href:'/log',route:'log',label:'Log',icon:'history'},
      {href:'/bin',route:'bin',label:'Bin',icon:'delete'},
    ]},
  ];
  return sections.filter(section=>section.items.length>0);
}

// Pages built around a grid run the full width of the panel, as in Google
// Photos; the Year calendar, Settings and Apple Photos keep a reading measure,
// since a calendar stretched across a wide screen is hard to read along a row.
const gridRoutes=new Set<LegacyRoute>(['today','dupes','upgrades','shadows','shots','social','log','bin','frame']);

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
  const suggestions=filters?matchFilters(filters,value):[];
  // / goes to the search, as in Google Photos; Esc in an empty field leaves it.
  useShortcut('/',()=>{input.current?.focus();input.current?.select()});
  function pick(suggestion:Suggestion){
    if(suggestion.kind==='filter')filters?.toggle(suggestion.option.id);
    else filters?.sort?.set(suggestion.option.id);
    setValue('');setActive(0);
    input.current?.focus();
  }
  function go(event:FormEvent){
    event.preventDefault();
    // On a phone the field is folded to its icon, so the icon opens it.
    if(!value.trim()){input.current?.focus();return}
    if(suggestions.length>0){pick(suggestions[Math.min(active,suggestions.length-1)]);return}
    const path=pathForDate(value);
    if(path)navigate(path);
    else setProblem(filters?'Type a date, such as 14 Aug 2019, or a filter, such as videos.':'Type a date, such as 14 Aug 2019, or 14 Aug for every year.');
  }
  function keys(event:ReactKeyboardEvent<HTMLInputElement>){
    if(suggestions.length>0&&(event.key==='ArrowDown'||event.key==='ArrowUp')){
      event.preventDefault();
      const by=event.key==='ArrowDown'?1:-1;
      setActive(current=>(current+by+suggestions.length)%suggestions.length);
    }else if(event.key==='Escape'&&value){
      setValue('');setActive(0);
    }else if(event.key==='Escape'){
      input.current?.blur();
    }else if(event.key==='Backspace'&&!value&&filters){
      // As in a field of chips, Backspace in an empty field takes the last
      // pill off: the order, which sits last, then the filters.
      const last=filters.options.filter(option=>option.on).at(-1);
      const usual=filters.sort?.options[0];
      if(unusualSort(filters)&&usual){event.preventDefault();filters.sort?.set(usual.id)}
      else if(last){event.preventDefault();filters.toggle(last.id)}
    }
  }
  const listing=suggestions.length>0;
  const on=(filters?.options.filter(option=>option.on).length??0)+(filters&&unusualSort(filters)?1:0);
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
      {!value&&<span className="searchkey"><Kbd keys="/"/></span>}
      {filters&&<FilterButton filters={filters} button={filterButton} open={open==='filters'} onToggle={()=>setOpen(current=>current==='filters'?null:'filters')}/>}
      {problem&&<p id="search-problem" className="searchproblem" role="alert">{problem}</p>}
      {/* oxlint-disable-next-line jsx-a11y/no-noninteractive-element-to-interactive-role -- the combobox's listbox, see the options below */}
      {listing&&<ul id="search-suggestions" className="searchsuggest" role="listbox" aria-label="Filters">
        {/* oxlint-disable-next-line jsx-a11y/click-events-have-key-events, jsx-a11y/no-noninteractive-element-to-interactive-role -- a combobox: focus stays in the field, whose arrow keys and Enter drive these options */}
        {suggestions.map((suggestion,index)=><li key={`${suggestion.kind}:${suggestion.option.id}`} id={`search-suggestion-${index}`} role="option" aria-selected={index===Math.min(active,suggestions.length-1)}
          onPointerDown={event=>event.preventDefault()} onClick={()=>pick(suggestion)} onPointerEnter={()=>setActive(index)}>
          {suggestion.kind==='filter'
            ?<><Icon name={suggestion.option.icon} filled={suggestion.option.on}/><span className="label">{suggestion.option.on?'Stop showing only':'Show only'} <b>{suggestion.option.label.toLowerCase()}</b></span><span className="n">{suggestion.option.count.toLocaleString()}</span></>
            :<><Icon name={suggestion.option.icon}/><span className="label">Show <b>{suggestion.option.label.toLowerCase()}</b></span></>}
        </li>)}
      </ul>}
    </form>
    {open==='date'&&date&&<DateCalendar date={date} anchor={pill} onClose={close}/>}
    {open==='filters'&&filters&&<FilterMenu filters={filters} anchor={filterButton} onClose={close}/>}
  </div>;
}

/** The app's frame: a top bar with the logo, the review streak and date
 * search, a sidebar of destinations ending in Settings, and the page on a
 * raised panel beside it. Below tablet width the sidebar folds on its own
 * into a rail of icons, so there is no menu button to reach for. route is
 * where the app is going, for the sidebar; shown is the page in the panel,
 * which is still the last one while it leaves. */
const narrowQuery='(max-width: 1000px)';

export function Layout({opening=false,moving=false,route,shown=route,path,visit,binFiles,reviewed,library,streak,notifications,onNotificationsRead,flash,children}:{opening?:boolean;moving?:boolean;route:LegacyRoute;shown?:LegacyRoute;path:string;visit:number;binFiles:number;reviewed?:{done:number;total:number};library?:LibraryTotals;streak?:{days:number;today:boolean};notifications?:number;onNotificationsRead?:()=>void;flash?:string;children:ReactNode}){
  const [selection,setSelection]=useState<Selection|null>(null);
  const addons=useAddons();
  const sections=useMemo(()=>sidebar(addons),[addons]);
  const [pageActions,setPageActions]=useState<PageActions|null>(null);
  const [pageDate,setPageDate]=useState<PageDate|null>(null);
  const [pageFilters,setPageFilters]=useState<PageFilters|null>(null);
  // Folded, the sidebar is a rail of icons whose labels become tooltips.
  const [rail,setRail]=useState(()=>matchMedia(narrowQuery).matches);
  useEffect(()=>{
    const query=matchMedia(narrowQuery);
    const change=()=>setRail(query.matches);
    query.addEventListener('change',change);
    return()=>query.removeEventListener('change',change);
  },[]);
  const share=reviewed&&reviewed.total>0?reviewed.done/reviewed.total:0;
  // The streak: its pill beside the logo, the calendar it opens, the opening
  // animation once a day, and a bump when today's first review lights it.
  const pill=useRef<HTMLButtonElement>(null);
  const [calendarOpen,setCalendarOpen]=useState(false);
  // Moving to another page closes whatever the frame had open for the last.
  const [shownVisit,setShownVisit]=useState(visit);
  if(shownVisit!==visit){setShownVisit(visit);setCalendarOpen(false)}
  const closeCalendar=useCallback(()=>setCalendarOpen(false),[]);
  const [intro,setIntro]=useState(false);
  const [bump,setBump]=useState(false);
  const introChecked=useRef(false);
  const litBefore=useRef<boolean|null>(null);
  // The streak's own opening waits for the app's, so the two never overlap.
  useEffect(()=>{
    if(!streak||opening)return;
    if(!introChecked.current){introChecked.current=true;if(introDue(streak))setIntro(true)}
    if(litBefore.current===false&&streak.today)setBump(true);
    litBefore.current=streak.today;
  },[streak,opening]);
  useEffect(()=>{if(!bump)return;const timer=setTimeout(()=>setBump(false),900);return()=>clearTimeout(timer)},[bump]);
  return <div className={`shell${rail?' side-hidden':''}`}>
    {selection?<SelectionBar selection={moving?{...selection,busy:true}:selection}/>:<header className="gbar">
      <div className="gbarstart">
        <a className="brand" href="/" {...tipProps('Year')}><Logo intro/><LogoMark className="brandmark"/></a>
        {streak&&<div className="streakwrap">
          <StreakPill streak={streak} pill={pill} open={calendarOpen} bump={bump} onToggle={()=>setCalendarOpen(open=>!open)}/>
          {calendarOpen&&<StreakCalendar streak={streak} anchor={pill} onClose={closeCalendar}/>}
        </div>}
        {notifications!==undefined&&<NotificationBell key={visit} unread={notifications} onRead={()=>onNotificationsRead?.()}/>}
        <TasksButton/>
      </div>
      <DateSearch key={visit} date={pageDate} filters={pageFilters}/>
      <div className="gbaracts">
        {pageActions&&<PageActionButtons page={moving?{...pageActions,actions:pageActions.actions.map(action=>({...action,disabled:true}))}:pageActions}/>}
        {!selection&&<GuideButton route={shown}/>}
      </div>
    </header>}
    <aside id="side" className="side">
      <nav aria-label="Main navigation">
        {sections.map(section=><div key={section.title??'library'} className="sidesec">
          {section.title&&<h2 className="sidetitle">{section.title}</h2>}
          {section.items.map(item=>{
            const on=item.route?route===item.route:path===item.href;
            return <a key={item.href} href={item.href} className={on?'on':undefined} aria-current={on?'page':undefined} {...(rail?tipProps(item.label):{})}>
              <Icon name={item.icon} filled={on}/><span className="sidelabel">{item.label}</span>
              {item.route==='bin'&&binFiles>0&&<span className="count" aria-label={`${binFiles.toLocaleString()} file${binFiles===1?'':'s'}`}>{binFiles.toLocaleString()}</span>}
            </a>;
          })}
        </div>)}
      </nav>
      {reviewed&&reviewed.total>0&&<a className="sideprogress" href="/year" style={{'--share':Math.min(1,share)} as CSSProperties}
        title={rail?`Reviewed ${reviewed.done.toLocaleString()} of ${reviewed.total.toLocaleString()} dates`:'Open the calendar'}>
        <span className="sideprogresshead"><Icon name="task_alt"/><span className="sidelabel">Reviewed</span></span>
        <ProgressBar className="meter" label="Calendar dates reviewed" value={reviewed.done} max={reviewed.total}/>
        <span className="sideprogressfoot"><span className="sideprogressnote">{reviewed.done.toLocaleString()} of {reviewed.total.toLocaleString()} dates</span></span>
      </a>}
      {library&&addons?.find(addon=>addon.id==='library-totals')?.on!==false&&library.photos.files+library.videos.files>0&&<SideLibrary library={library} rail={rail}/>}
      <nav className="sidefoot" aria-label="Settings">
        <a href="/addons" className={route==='addons'?'on':undefined} aria-current={route==='addons'?'page':undefined} {...(rail?tipProps('Addons'):{})}>
          <Icon name="extension" filled={route==='addons'}/><span className="sidelabel">Addons</span>
        </a>
        <a href="/settings" className={route==='settings'?'on':undefined} aria-current={route==='settings'?'page':undefined} {...(rail?tipProps('Settings'):{})}>
          <Icon name="settings" filled={route==='settings'}/><span className="sidelabel">Settings</span>
        </a>
      </nav>
    </aside>
    <GridZoom enabled={gridRoutes.has(shown)}/>
    <Tips/>
    {intro&&streak&&<StreakIntro streak={streak} target={pill} onDone={()=>{setIntro(false);setBump(true)}}/>}
    <div className="panel">
      {flash&&<p className="flash" role="status">{flash}</p>}
      <main className={`${gridRoutes.has(shown)?'wide':''}${selection?' selecting':''}`||undefined}><SelectionProvider value={setSelection}><PageActionsProvider value={setPageActions}><PageDateProvider value={setPageDate}><PageFiltersProvider value={setPageFilters}>{children}</PageFiltersProvider></PageDateProvider></PageActionsProvider></SelectionProvider></main>
      <div className="snacks" id="snacks"/>
    </div>
  </div>;
}
