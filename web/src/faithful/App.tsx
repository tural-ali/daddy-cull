import {Library} from './Library';
import {Fragment,useCallback,useEffect,useLayoutEffect,useRef,useState,type ReactNode} from 'react';
import {Layout,type LegacyRoute} from './Layout';
import {PageGuide,syncGuides} from './PageGuide';
import {dayName} from './goto';
import {Today,type TodayData} from './Today';
import {Year,type YearData} from './Year';
import {OpeningContext,YearOpening,opensWithLogo,useOpening} from './YearOpening';
import {Duplicates,type DuplicateReport} from './Duplicates';
import {Settings,type Stats} from './Settings';
import {setVideoSoundPreference} from '../SessionVideo';
import {Log,logPage,type HistoryEvent} from './Log';
import {Bin} from './Bin';
import {Screenshots,type ScreenshotPage} from './Screenshots';
import {Shadows} from './Shadows';
import {Social,type SocialPage} from './Social';
import {Upgrades,type UpgradePage} from './Upgrades';
import {Photos} from './Photos';
import {GooglePhotos,type GooglePhotosPage} from './GooglePhotos';
import {Addons} from './Addons';
import {Developers} from './Developers';
import {Setup,readSetup} from './Setup';
import {AddonFrame,AddonMissing} from './AddonFrame';
import {currentAddons,framePage,ownerOf,readAddons,useAddons} from './addonList';
import {recoverPending} from '../recoverPending';
import {BIN_CHANGED} from '../api';
import {pagePath} from './photoURL';
import {Busy} from '../Busy';
import {ProgressBar} from '../ProgressBar';
import {Snacks} from './Snacks';
import {CATALOGUE_CHANGED,catalogueGeneration,quietEnough,watchCatalogue} from './catalogueWatch';
import {NAVIGATED,RELOAD_PAGE,currentVisit,followLinks,navigate,reloadPage,savedScroll} from './router';
import {settled} from '../saving';
import {LEAVE,LONG,ZOOM,calm,growSquare,squareOrigin,staggerGrid,staggerVisible,takeZoom} from './pageMotion';

type Loaded={route:LegacyRoute;content:ReactNode};
/** The page in the panel. key names the visit it was read for, so a page
 * kept on screen while it leaves is not drawn afresh. phase is where it is in
 * a move: leaving as the next is read, entering once drawn, or shown; motion
 * says how: out of a date's square (zoom), back through the history (back),
 * or from a day to the year, onto that day's square (fromday). */
type PageState=Loaded&{key:string;phase:'shown'|'waiting'|'leaving'|'entering';motion?:'zoom'|'back'|'fromday';origin?:string;md?:string};
/** The page the app is on: its path, which visit to it this is, and whether
 * the visit was reached with Back or Forward rather than a link. */
type Place={path:string;visit:number;returned:boolean};

// The server counts a day of review in the viewer's own time zone.
const zone=encodeURIComponent(Intl.DateTimeFormat().resolvedOptions().timeZone||'');

async function json<T>(url:string):Promise<T>{
  const response=await fetch(url);
  if(!response.ok)throw new Error('The catalogue could not load this page.');
  return response.json() as Promise<T>;
}

function routeFor(path:string):LegacyRoute{
  if(path==='/library')return 'library';
  if(path==='/year')return 'year';
  if(path==='/duplicates')return 'dupes';
  if(path==='/upgrades')return 'upgrades';
  if(path==='/shadows')return 'shadows';
  if(path==='/screenshots')return 'shots';
  if(path==='/social')return 'social';
  if(path==='/photos')return 'photos';
  if(path==='/google-photos')return 'google';
  if(path==='/log')return 'log';
  if(path==='/bin')return 'bin';
  if(path==='/settings')return 'settings';
  if(path==='/addons')return 'addons';
  if(path==='/developers')return 'developers';
  if(path==='/setup')return 'setup';
  if(path.startsWith('/addons/'))return 'frame';
  return 'today';
}

// Cull's own pages that belong to an addon.
const addonPaths=new Set(['/screenshots','/social','/shadows','/upgrades','/photos','/google-photos']);

const routeTitles:Record<LegacyRoute,string>={library:'Library',today:'Today',year:'Year',dupes:'Duplicates',upgrades:'Upgrades',shadows:'Shadowed',shots:'Screenshots',social:'Saved from social',photos:'Apple Photos',google:'Google Photos',log:'Log',bin:'Bin',settings:'Settings',addons:'Addons',developers:'Developers',setup:'Set up',frame:'Addon'};

/** The browser tab names the page, and the date for a day, so several open
 * tabs can be told apart. */
function titleFor(path:string){
  const on=path.match(/^\/on\/(\d{2})-(\d{2})$/);
  if(on)return new Date(2024,+on[1]-1,+on[2]).toLocaleDateString('en-GB',{day:'numeric',month:'long'});
  if(/^\/day\/\d{4}-\d{2}-\d{2}$/.test(path))return dayName(path);
  return routeTitles[routeFor(path)];
}

/** The page's path for the address the browser is on. The bare address is
 * the year, and /today is today's date, and each says so. An open photo's
 * address is its page's address plus /photo/<id>; the page itself reads the
 * photo, the frame only needs the page. */
function currentPath(){
  if(location.pathname==='/')history.replaceState(history.state,'',`/year${location.search}${location.hash}`);
  if(location.pathname==='/today'){
    const now=new Date();
    const md=`${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`;
    history.replaceState(history.state,'',`/on/${md}${location.search}${location.hash}`);
  }
  return pagePath();
}

/** What the panel shows while the next page is read: the frame stays, and a
 * moment later, if the page is still on its way, the brand tiles say which it is. */
function Opening({label}:{label:string}){
  return <div className="pageload"><Busy size={64} label={label}/></div>;
}

export function App(){
  const [place,setPlace]=useState<Place>(()=>({path:currentPath(),visit:currentVisit(),returned:false}));
  const path=place.path;
  const [stats,setStats]=useState<Stats|null>(null);
  const [page,setPage]=useState<PageState>(()=>({route:routeFor(path),content:<Opening label="Opening the catalogue…"/>,key:'start',phase:'shown'}));
  const stage=useRef<HTMLDivElement>(null);
  // The page last drawn, for the year to zoom out onto the day it came from.
  const drawnPath=useRef<string|null>(null);
  const [error,setError]=useState('');
  const [pending,setPending]=useState(false);
  const [recovered,setRecovered]=useState(false);
  // A page load at the bare address opens with the logo, which the camera
  // dives through once the year has been read.
  const [opensOnYear]=useState(opensWithLogo);
  const [yearShown,setYearShown]=useState(false);
  const opening=useOpening(opensOnYear,yearShown,error!==''||place.path!=='/year');
  // The archive as the page was read: the catalogue's generation then, and
  // whether anyone has done anything on the page since, which a fresh read
  // would take away with its undo history.
  const generation=useRef<number|null>(null);
  const touched=useRef(false);
  const announced=useRef<number|null>(null);
  const [version,setVersion]=useState(0);
  const [notice,setNotice]=useState<'refreshed'|'stale'|null>(null);
  const keepScroll=useRef<number|null>(null);
  // Where to put the scroll once a page just moved to has drawn: the top, the
  // place Back returns to, or the part of the page the address names.
  const arrival=useRef<number|'hash'|null>(null);
  const first=useRef(true);
  useEffect(()=>{document.title=`${titleFor(path)} · Daddy, Cull!`},[path]);
  // Links and Back move within the app; the page changes, the frame does not.
  useEffect(()=>{
    const moved=(returned:boolean)=>()=>{
      const visit=currentVisit();
      if(visit===place.visit&&!returned)return;
      const next=currentPath();
      setPlace(current=>current.visit===visit?current:{path:next,visit,returned});
    };
    const pushed=moved(false),popped=moved(true);
    window.addEventListener(NAVIGATED,pushed);
    window.addEventListener('popstate',popped);
    const stop=followLinks();
    return()=>{window.removeEventListener(NAVIGATED,pushed);window.removeEventListener('popstate',popped);stop()};
  },[place.visit]);
  // The guides hidden in the catalogue, as each read of the stats has them.
  const statsRead=stats!==null,guidesHidden=stats?.hiddenGuides;
  useEffect(()=>{if(statsRead)syncGuides(guidesHidden)},[statsRead,guidesHidden]);
  const readStats=useCallback(()=>json<Stats>(`/api/stats?tz=${zone}`).then(setStats).catch(()=>{}),[]);
  useEffect(()=>{
    recoverPending().then(()=>setRecovered(true)).catch(reason=>setError((reason as Error).message));
  },[]);
  useEffect(()=>{
    if(!recovered)return;
    const controller=new AbortController();
    json<Stats>(`/api/stats?tz=${zone}`).then(next=>{setVideoSoundPreference(next.videoMuted!==false);setStats(next)}).catch(reason=>{if(!controller.signal.aborted)setError((reason as Error).message)});
    // The badge is read again whenever a page says the Bin may have changed.
    // A burst of saves collapses into one read, and a failed read keeps the
    // last count rather than blanking the page.
    let timer=0;
    const reread=()=>{
      clearTimeout(timer);
      timer=window.setTimeout(()=>{void readStats()},250);
    };
    const worked=()=>{touched.current=true};
    window.addEventListener(BIN_CHANGED,reread);
    window.addEventListener(BIN_CHANGED,worked);
    window.addEventListener(CATALOGUE_CHANGED,reread);
    return()=>{controller.abort();clearTimeout(timer);window.removeEventListener(BIN_CHANGED,reread);window.removeEventListener(BIN_CHANGED,worked);window.removeEventListener(CATALOGUE_CHANGED,reread)};
  },[recovered,readStats]);
  const load=useCallback(async():Promise<Loaded>=>{
    const route=routeFor(path);
      // A page that belongs to an addon shows only while the addon is on.
      if(route==='frame'||ownerOf(currentAddons(),path)||addonPaths.has(path)){
        await readAddons();
        const addons=currentAddons();
        if(route==='frame'){
          const found=framePage(addons,path);
          if(!found||!found.addon.on)return {route,content:<AddonMissing addon={found?.addon} label={found?.page.label??'This page'}/>};
          return {route,content:<AddonFrame addon={found.addon} page={found.page}/>};
        }
        const owner=ownerOf(addons,path);
        if(owner&&!owner.on)return {route,content:<AddonMissing addon={owner} label={routeTitles[route]}/>};
      }
      if(path==='/library')return {route,content:<Library initial={await json<import('../api').Page>(`/api/search${location.search||'?limit=50'}`)}/>};
      if(path==='/addons')return {route,content:<Addons/>};
      if(path==='/developers')return {route,content:<Developers/>};
      if(path==='/setup')return {route,content:<Setup initial={await readSetup()}/>};
      if(path.startsWith('/on/')){
        const md=path.slice(4);
        const data=await json<TodayData>(`/api/today/${md}`);
        return {route,content:<Today initial={data}/>};
      }
      if(/^\/day\/\d{4}-\d{2}-\d{2}$/.test(path)){
        const day=path.slice(5),md=day.slice(5),data=await json<TodayData>(`/api/today/${md}`);
        const years=data.years.filter(year=>year.day===day);
        const oneDay={...data,years,memories:years.reduce((sum,year)=>sum+year.assets.length,0),bytes:years.reduce((sum,year)=>sum+year.bytes,0)};
        return {route:'today',content:<Today initial={oneDay}/>};
      }
      if(path==='/year')return {route,content:<Year {...await json<YearData>(`/api/year?tz=${zone}`)}/>};
      // Coverage against the files that could possibly be duplicates is what makes
      // an empty result readable: no groups found is a different statement from no
      // groups because nothing was ever hashed.
      if(path==='/duplicates')return {route,content:<Duplicates report={await json<DuplicateReport>('/api/duplicate-report?paged=1&limit=50')}/>};
      if(path==='/upgrades')return {route,content:<Upgrades initial={await json<UpgradePage>('/api/upgrades')}/>};
      if(path==='/log')return {route,content:<Log initial={await json<HistoryEvent[]>(`/api/log?limit=${logPage}`)}/>};
      if(path==='/photos')return {route,content:<Photos/>};
      if(path==='/google-photos'){
        const tab=new URLSearchParams(location.search).get('tab')||'missing';
        return {route,content:<GooglePhotos initial={await json<GooglePhotosPage>(`/api/google-photos?tab=${encodeURIComponent(tab)}&from=0`)}/>};
      }
      if(path==='/bin')return {route,content:<Bin onCount={count=>setStats(current=>current&&{...current,bin:count})}/>};
      if(path==='/settings')return {route,content:<Settings stats={await json<Stats>(`/api/stats?tz=${zone}`)}/>};
      if(path==='/shadows')return {route,content:<Shadows groups={await json<Parameters<typeof Shadows>[0]['groups']>('/api/shadows')}/>};
      if(path==='/social'){
        const band=new URLSearchParams(location.search).get('band')||'';
        return {route,content:<Social page={await json<SocialPage>(`/api/social?band=${encodeURIComponent(band)}&from=0`)} band={band}/>};
      }
      if(path==='/screenshots'){
        const params=new URLSearchParams(location.search),filter=params.get('show')||'',review=params.get('review')==='reviewed'||params.get('review')==='all'?params.get('review')!:'';
        return {route,content:<Screenshots page={await json<ScreenshotPage>(`/api/screenshots?kind=${encodeURIComponent(filter)}&review=${review}&from=0`)} filter={filter} review={review}/>};
      }
      throw new Error('This legacy workflow has not been connected yet.');
  },[path]);
  useEffect(()=>{
    // A decision replayed from a closed tab changes what the page should show,
    // so nothing loads until recovery has finished, and then it loads once.
    if(!recovered)return;
    let active=true;
    const moving=!first.current;
    first.current=false;
    setError('');setNotice(null);setPending(moving);
    touched.current=false;
    // Keep the current page while the next is read. A delayed status floats
    // over it; only a ready destination starts the short departure. A date
    // still opens immediately out of its square, which bridges slow reads.
    let left:Promise<void>=Promise.resolve();
    let leaveTimer:ReturnType<typeof setTimeout>|undefined;
    let square:ReturnType<typeof growSquare>|null=null;
    const still=calm();
    const zoom=moving?takeZoom(place.path):null;
    const leave=(duration:number)=>new Promise<void>(resolve=>{leaveTimer=setTimeout(resolve,duration)});
    if(moving&&zoom&&!still){
      square=growSquare(zoom);
      const box=stage.current?.getBoundingClientRect();
      const origin=box?`${zoom.rect.left-box.left+zoom.rect.width/2}px ${zoom.rect.top-box.top+zoom.rect.height/2}px`:undefined;
      setPage(current=>({...current,phase:'leaving',motion:'zoom',origin}));
      left=leave(ZOOM);
    }else if(moving){
      setPage(current=>({...current,phase:'waiting',motion:undefined,origin:undefined}));
    }
    (async()=>{
      // A choice still being saved on the page just left, or one kept in a
      // journal, lands before the next page is read, as it did when every
      // page was a fresh load.
      if(moving){await settled();await recoverPending();void readStats()}
      // The generation is read before the page, so a change that lands while
      // the page loads is caught at the next check rather than missed. A Cull
      // set up from a config file that has not been through setup opens on
      // it, from whichever address it was opened at.
      const [next,setup]=await Promise.all([catalogueGeneration(),!moving&&place.path!=='/setup'?readSetup().catch(()=>null):null]);
      if(setup?.configurable&&!setup.config.done){
        if(active){history.replaceState(history.state,'','/setup');navigate('/setup')}
        return;
      }
      generation.current=next;
      const result=await load();
      if(!active)return;
      if(moving&&!still&&!square){
        setPage(current=>({...current,phase:'leaving',motion:place.returned?'back':undefined}));
        left=leave(LEAVE);
      }
      await left;
      if(!active)return;
      setPending(false);
      arrival.current=place.returned?savedScroll()??0:location.hash?'hash':moving?0:null;
      const came=drawnPath.current?.match(/^\/(?:on|day\/\d{4})[/-](\d{2}-\d{2})$/);
      // Sidebar navigation uses the shared transition regardless of the page
      // left. The calendar zoom-out belongs to a history return from a day.
      const motion=square?'zoom':place.returned&&came&&result.route==='year'?'fromday':place.returned?'back':undefined;
      setPage({...result,key:String(place.visit),phase:moving&&!still?'entering':'shown',motion,md:came?.[1]});
      drawnPath.current=place.path;
      square?.fade();
      square=null;
      if(result.route==='year')setYearShown(true);
    })().catch(reason=>{if(active){square?.remove();setPending(false);setError((reason as Error).message)}});
    return()=>{active=false;clearTimeout(leaveTimer);square?.remove()};
  },[load,recovered,place,readStats]);
  // A page coming in rises into place, and once it has, it is left with no
  // motion on it at all, so nothing inside is drawn relative to a moving box.
  // Out of a date, its photographs come in one after another; from a day,
  // the year zooms out onto that day's square.
  useLayoutEffect(()=>{
    if(page.phase!=='entering'||!stage.current)return;
    const unstagger=page.motion==='zoom'?staggerGrid(stage.current):page.motion==='fromday'?undefined:staggerVisible(stage.current);
    if(page.motion==='fromday'&&page.md){
      const origin=squareOrigin(stage.current,page.md);
      if(origin)stage.current.style.transformOrigin=origin;
    }
    const key=page.key,el=stage.current;
    // A long page fades in rather than rising, as moving it would have the
    // browser paint the whole of it; see legacy.css.
    if(!page.motion||page.motion==='back')el.style.animationName=el.scrollHeight>innerHeight*LONG?'page-fade-in':'';
    const timer=setTimeout(()=>{
      el.style.transformOrigin='';
      el.style.animationName='';
      setPage(current=>current.key===key&&current.phase==='entering'?{...current,phase:'shown',motion:undefined,origin:undefined}:current);
    },page.motion==='zoom'?1000:page.motion==='fromday'?520:560);
    return()=>{clearTimeout(timer);unstagger?.();el.style.animationName=''};
  },[page.phase,page.key,page.motion,page.md]);
  // Keys meant for the page left do nothing while it goes, from the moment
  // it starts to.
  useLayoutEffect(()=>{
    if(page.phase!=='leaving'&&page.phase!=='waiting')return;
    const swallow=(event:KeyboardEvent)=>{
      if(page.phase==='waiting'&&(event.key==='Tab'||event.key==='Escape'||(event.metaKey||event.ctrlKey)&&!['z','u'].includes(event.key.toLowerCase())||event.altKey||event.target instanceof Element&&event.target.closest('input,textarea,select,[contenteditable="true"]')))return;
      event.stopImmediatePropagation();event.preventDefault();
    };
    window.addEventListener('keydown',swallow,{capture:true});
    return()=>window.removeEventListener('keydown',swallow,{capture:true});
  },[page.phase]);
  useLayoutEffect(()=>{
    const to=arrival.current;
    if(to===null)return;
    arrival.current=null;
    if(to==='hash')document.getElementById(decodeURIComponent(location.hash.slice(1)))?.scrollIntoView();
    else window.scrollTo(0,to);
  },[page]);

  // Read the page again in place, where it was scrolled to. A read that fails
  // leaves the page as it was, and the next check tries again.
  const refresh=useCallback(async(next:number)=>{
    let result:Loaded;
    try{result=await load()}catch{return}
    generation.current=next;
    touched.current=false;
    keepScroll.current=window.scrollY;
    setPage(current=>({...result,key:current.key,phase:'shown'}));
    setVersion(current=>current+1);
    setNotice(document.hidden?null:'refreshed');
  },[load]);
  useLayoutEffect(()=>{
    if(keepScroll.current===null)return;
    window.scrollTo(0,keepScroll.current);
    keepScroll.current=null;
  },[version]);
  useEffect(()=>{
    if(!recovered)return;
    let checking=false;
    const check=async()=>{
      if(checking)return;
      checking=true;
      try{
        const next=await catalogueGeneration();
        if(next===null)return;
        if(generation.current===null){generation.current=next;return}
        if(next===generation.current)return;
        if(announced.current!==next){announced.current=next;window.dispatchEvent(new Event(CATALOGUE_CHANGED))}
        // Someone has been working on this page: a fresh read would take
        // their undo history away, so it waits for them to ask.
        if(touched.current){setNotice('stale');return}
        // A photo open, a dialog or a half-typed date: the next check tries again.
        if(!quietEnough())return;
        await refresh(next);
      }finally{checking=false}
    };
    return watchCatalogue(()=>{void check()});
  },[recovered,refresh]);
  useEffect(()=>{
    if(notice!=='refreshed')return;
    const timer=setTimeout(()=>setNotice(null),6000);
    return()=>clearTimeout(timer);
  },[notice]);
  const refreshNow=async()=>{
    const next=await catalogueGeneration();
    if(next!==null)await refresh(next);
  };
  // A page that asks to be read again, after an undo say, is redrawn where it
  // is. If it cannot be read, the whole app is loaded again rather than left
  // showing what is no longer so.
  useEffect(()=>{
    const again=()=>{void (async()=>{
      await settled();
      const next=await catalogueGeneration().catch(()=>null);
      let result:Loaded;
      try{result=await load()}catch{location.reload();return}
      if(next!==null)generation.current=next;
      touched.current=false;
      keepScroll.current=window.scrollY;
      setPage(current=>({...result,key:current.key,phase:'shown'}));
      setVersion(current=>current+1);
      void readStats();
    })()};
    window.addEventListener(RELOAD_PAGE,again);
    return()=>window.removeEventListener(RELOAD_PAGE,again);
  },[load,readStats]);
  // An addon turned on or off, here or in another tab, changes whether the
  // page it owns can show, so that page is drawn again at once.
  const addons=useAddons();
  const gate=addons===null?null:`${path}|${(()=>{
    if(routeFor(path)==='frame'){const found=framePage(addons,path);return found?`${found.addon.on}:${found.page.url}`:'none'}
    return String(ownerOf(addons,path)?.on??'none');
  })()}`;
  const lastGate=useRef<string|null>(null);
  useEffect(()=>{
    const previous=lastGate.current;
    lastGate.current=gate;
    if(previous&&gate&&previous!==gate&&previous.startsWith(`${path}|`))reloadPage();
  },[gate,path]);
  return <OpeningContext value={opening}><Layout opening={opening!==null} moving={pending} route={routeFor(place.path)} shown={page.route} path={place.path} visit={place.visit} binFiles={stats?.bin??stats?.marked??0} reviewed={stats?.calendarDates?{done:stats.reviewedDates??0,total:stats.calendarDates}:undefined} library={stats?.library} streak={stats?.streak!==undefined?{days:stats.streak,today:!!stats.reviewedToday}:undefined} notifications={stats?.notifications} onNotificationsRead={()=>setStats(current=>current&&{...current,notifications:0})}>{error?<><PageGuide route={page.route}/><p className="note warn" role="alert">{error} <button className="btn small" onClick={()=>location.reload()}>Retry</button></p></>:<div ref={stage} className={['pagestage',page.phase!=='shown'&&page.phase,page.phase!=='shown'&&page.motion].filter(Boolean).join(' ')} inert={page.phase==='leaving'||page.phase==='waiting'} style={page.phase==='leaving'&&page.origin?{transformOrigin:page.origin}:undefined}><PageGuide route={page.route}/><Fragment key={`${page.key}:${version}`}>{page.content}</Fragment></div>}
    {!error&&pending&&<div className="navigationload" key={place.visit}>
      <Busy label={`Opening ${titleFor(place.path)}…`}/><ProgressBar label="Opening page" decorative/>
    </div>}
    {notice&&<Snacks><div className="snack" role="status">{notice==='refreshed'?'Updated with new files from the archive.':<>New files arrived in the archive. <button type="button" className="snackact" onClick={()=>void refreshNow()}>Refresh</button></>}</div></Snacks>}
  </Layout>{opening&&<YearOpening phase={opening}/>}</OpeningContext>;
}
