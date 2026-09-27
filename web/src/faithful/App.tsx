import {useEffect,useState,type ReactNode} from 'react';
import {Layout,type LegacyRoute} from './Layout';
import {dayName} from './goto';
import {Today,type TodayData} from './Today';
import {Year,type YearData} from './Year';
import {Duplicates,type DuplicateGroup,type DuplicateReport} from './Duplicates';
import {Settings,type Stats} from './Settings';
import {setVideoSoundPreference} from '../SessionVideo';
import {Log,type HistoryEvent} from './Log';
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
    window.addEventListener(BIN_CHANGED,reread);
    return()=>{controller.abort();clearTimeout(timer);window.removeEventListener(BIN_CHANGED,reread)};
  },[recovered]);
  useEffect(()=>{
    // A decision replayed from a closed tab changes what the page should show,
    // so nothing loads until recovery has finished, and then it loads once.
    if(!recovered)return;
    let active=true;
    setError('');
    const route=routeFor(path);
    async function load(){
      if(path.startsWith('/on/')){
        const md=path.slice(4);
        const data=await json<TodayData>(`/api/today/${md}`);
        return {route,content:<Today initial={data}/>} as PageState;
      }
      if(/^\/day\/\d{4}-\d{2}-\d{2}$/.test(path)){
        const day=path.slice(5),md=day.slice(5),data=await json<TodayData>(`/api/today/${md}`);
        const years=data.years.filter(year=>year.day===day);
        const oneDay={...data,years,memories:years.reduce((sum,year)=>sum+year.assets.length,0),bytes:years.reduce((sum,year)=>sum+year.bytes,0)};
        return {route:'today',content:<Today initial={oneDay}/>} as PageState;
      }
      if(path==='/year')return {route,content:<Year {...await json<YearData>(`/api/year?tz=${zone}`)}/>} as PageState;
      // Coverage against the files that could possibly be duplicates is what makes
      // an empty result readable: no groups found is a different statement from no
      // groups because nothing was ever hashed.
      if(path==='/duplicates')return {route,content:<Duplicates report={await json<DuplicateReport>('/api/duplicate-report?limit=1000')}/>} as PageState;
      if(path==='/upgrades')return {route,content:<Upgrades initial={await json<UpgradePage>('/api/upgrades')}/>} as PageState;
      if(path==='/log')return {route,content:<Log initial={await json<HistoryEvent[]>('/api/log?limit=200')}/>} as PageState;
      if(path==='/photos')return {route,content:<Photos/>} as PageState;
      if(path==='/bin')return {route,content:<Bin onCount={count=>setStats(current=>current&&{...current,bin:count})}/>} as PageState;
      if(path==='/settings')return {route,content:<Settings stats={await json<Stats>(`/api/stats?tz=${zone}`)}/>} as PageState;
      if(path==='/shadows')return {route,content:<Shadows groups={await json<Parameters<typeof Shadows>[0]['groups']>('/api/shadows')}/>} as PageState;
      if(path==='/social'){
        const params=new URLSearchParams(location.search),band=params.get('band')||'',from=Math.max(0,Number.parseInt(params.get('from')||'0',10)||0);
        return {route,content:<Social page={await json<SocialPage>(`/api/social?band=${encodeURIComponent(band)}&from=${from}`)} band={band} from={from}/>} as PageState;
      }
      if(path==='/screenshots'){
        const params=new URLSearchParams(location.search),filter=params.get('show')||'',review=params.get('review')==='reviewed'?'reviewed':'',from=Math.max(0,Number.parseInt(params.get('from')||'0',10)||0);
        return {route,content:<Screenshots page={await json<ScreenshotPage>(`/api/screenshots?kind=${encodeURIComponent(filter)}&review=${review}&from=${from}`)} filter={filter} review={review} from={from}/>} as PageState;
      }
      throw new Error('This legacy workflow has not been connected yet.');
    }
    load().then(result=>{if(active)setPage(result)}).catch(reason=>{if(active)setError((reason as Error).message)});
    return()=>{active=false};
  },[path,recovered]);
  return <Layout route={page.route} binFiles={stats?.bin??stats?.marked??0} reviewed={stats?.calendarDates?{done:stats.reviewedDates??0,total:stats.calendarDates}:undefined} streak={stats?.streak!==undefined?{days:stats.streak,today:!!stats.reviewedToday}:undefined}>{error?<p className="note warn" role="alert">{error} <button className="btn small" onClick={()=>location.reload()}>Retry</button></p>:page.content}</Layout>;
}
