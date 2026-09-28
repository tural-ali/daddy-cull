import {Fragment,useCallback,useEffect,useLayoutEffect,useRef,useState,type ReactNode} from 'react';
import {Layout,type LegacyRoute} from './Layout';
import {dayName} from './goto';
import {Today,type TodayData} from './Today';
import {Year,type YearData} from './Year';
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
import {recoverPending} from '../recoverPending';
import {BIN_CHANGED} from '../api';
import {pagePath} from './photoURL';
import {Busy} from '../Busy';
import {Snacks} from './Snacks';
import {CATALOGUE_CHANGED,catalogueGeneration,quietEnough,watchCatalogue} from './catalogueWatch';

type PageState={route:LegacyRoute;content:ReactNode};

// The server counts a day of review in the viewer's own time zone.
const zone=encodeURIComponent(Intl.DateTimeFormat().resolvedOptions().timeZone||'');

async function json<T>(url:string):Promise<T>{
  const response=await fetch(url);
  if(!response.ok)throw new Error('The catalogue could not load this page.');
  return response.json() as Promise<T>;
}

function routeFor(path:string):LegacyRoute{
  if(path==='/year')return 'year';
  if(path==='/duplicates')return 'dupes';
  if(path==='/upgrades')return 'upgrades';
  if(path==='/shadows')return 'shadows';
  if(path==='/screenshots')return 'shots';
  if(path==='/social')return 'social';
  if(path==='/photos')return 'photos';
  if(path==='/log')return 'log';
  if(path==='/bin')return 'bin';
  if(path==='/settings')return 'settings';
  return 'today';
}

const routeTitles:Record<LegacyRoute,string>={today:'Today',year:'Year',dupes:'Duplicates',upgrades:'Upgrades',shadows:'Shadowed',shots:'Screenshots',social:'Saved from social',photos:'Apple Photos',log:'Log',bin:'Bin',settings:'Settings'};

/** The browser tab names the page, and the date for a day, so several open
 * tabs can be told apart. */
function titleFor(path:string){
  const on=path.match(/^\/on\/(\d{2})-(\d{2})$/);
  if(on)return new Date(2024,+on[1]-1,+on[2]).toLocaleDateString('en-GB',{day:'numeric',month:'long'});
  if(/^\/day\/\d{4}-\d{2}-\d{2}$/.test(path))return dayName(path);
  return routeTitles[routeFor(path)];
}

export function App(){
  const now=new Date();
  const currentMD=`${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`;
  // An open photo's address is its page's address plus /photo/<id>; the page
  // itself reads the photo, the frame only needs the page.
  const initialPath=location.pathname==='/'?`/on/${currentMD}`:pagePath();
  const [path]=useState(initialPath);
  const [stats,setStats]=useState<Stats|null>(null);
  const [page,setPage]=useState<PageState>({route:routeFor(initialPath),content:<Busy size={64} label="Opening the catalogue…"/>});
  const [error,setError]=useState('');
  const [recovered,setRecovered]=useState(false);
  // The archive as the page was read: the catalogue's generation then, and
  // whether anyone has done anything on the page since, which a fresh read
  // would take away with its undo history.
  const generation=useRef<number|null>(null);
  const touched=useRef(false);
  const announced=useRef<number|null>(null);
  const [version,setVersion]=useState(0);
  const [notice,setNotice]=useState<'refreshed'|'stale'|null>(null);
  const keepScroll=useRef<number|null>(null);
  useEffect(()=>{if(location.pathname==='/')history.replaceState(null,'',initialPath)},[initialPath]);
  useEffect(()=>{document.title=`${titleFor(path)} · Daddy, Cull!`},[path]);
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
      timer=window.setTimeout(()=>{json<Stats>(`/api/stats?tz=${zone}`).then(setStats).catch(()=>{})},250);
    };
    const worked=()=>{touched.current=true};
    window.addEventListener(BIN_CHANGED,reread);
    window.addEventListener(BIN_CHANGED,worked);
    window.addEventListener(CATALOGUE_CHANGED,reread);
    return()=>{controller.abort();clearTimeout(timer);window.removeEventListener(BIN_CHANGED,reread);window.removeEventListener(BIN_CHANGED,worked);window.removeEventListener(CATALOGUE_CHANGED,reread)};
  },[recovered]);
  const load=useCallback(async():Promise<PageState>=>{
    const route=routeFor(path);
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
      if(path==='/duplicates')return {route,content:<Duplicates report={await json<DuplicateReport>('/api/duplicate-report?limit=1000')}/>};
      if(path==='/upgrades')return {route,content:<Upgrades initial={await json<UpgradePage>('/api/upgrades')}/>};
      if(path==='/log')return {route,content:<Log initial={await json<HistoryEvent[]>(`/api/log?limit=${logPage}`)}/>};
      if(path==='/photos')return {route,content:<Photos/>};
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
    setError('');
    // The generation is read before the page, so a change that lands while
    // the page loads is caught at the next check rather than missed.
    catalogueGeneration().then(async next=>{
      generation.current=next;
      const result=await load();
      if(active)setPage(result);
    }).catch(reason=>{if(active)setError((reason as Error).message)});
    return()=>{active=false};
  },[load,recovered]);

  // Read the page again in place, where it was scrolled to. A read that fails
  // leaves the page as it was, and the next check tries again.
  const refresh=useCallback(async(next:number)=>{
    let result:PageState;
    try{result=await load()}catch{return}
    generation.current=next;
    touched.current=false;
    keepScroll.current=window.scrollY;
    setPage(result);
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
  return <Layout route={page.route} binFiles={stats?.bin??stats?.marked??0} reviewed={stats?.calendarDates?{done:stats.reviewedDates??0,total:stats.calendarDates}:undefined} streak={stats?.streak!==undefined?{days:stats.streak,today:!!stats.reviewedToday}:undefined} notifications={stats?.notifications} onNotificationsRead={()=>setStats(current=>current&&{...current,notifications:0})}>{error?<p className="note warn" role="alert">{error} <button className="btn small" onClick={()=>location.reload()}>Retry</button></p>:<Fragment key={version}>{page.content}</Fragment>}
    {notice&&<Snacks><div className="snack" role="status">{notice==='refreshed'?'Updated with new files from the archive.':<>New files arrived in the archive. <button type="button" className="snackact" onClick={()=>void refreshNow()}>Refresh</button></>}</div></Snacks>}
  </Layout>;
}
