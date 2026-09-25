import {useEffect,useState,type ReactNode} from 'react';
import {Layout,type LegacyRoute} from './Layout';
import {Today,type TodayData} from './Today';
import {Year,type YearData} from './Year';
import {Duplicates,type DuplicateGroup,type DuplicateReport} from './Duplicates';
import {Settings,type Stats} from './Settings';
import {Log,type HistoryEvent} from './Log';
import {Bin} from './Bin';
import {Screenshots,type ScreenshotPage} from './Screenshots';
import {Shadows} from './Shadows';
import {Social,type SocialPage} from './Social';
import {Upgrades,type UpgradePage} from './Upgrades';
import {recoverPending} from '../recoverPending';
import {BIN_CHANGED} from '../api';
import {Busy} from '../Busy';

type PageState={route:LegacyRoute;content:ReactNode};

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
  if(path==='/log')return 'log';
  if(path==='/bin')return 'bin';
  if(path==='/settings')return 'settings';
  return 'today';
}

export function App(){
  const now=new Date();
  const currentMD=`${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`;
  const initialPath=location.pathname==='/'?`/on/${currentMD}`:location.pathname;
  const [path]=useState(initialPath);
  const [stats,setStats]=useState<Stats|null>(null);
  const [page,setPage]=useState<PageState>({route:routeFor(initialPath),content:<Busy size={64} label="Opening the catalogue…"/>});
  const [error,setError]=useState('');
  const [recovered,setRecovered]=useState(false);
  useEffect(()=>{if(location.pathname==='/')history.replaceState(null,'',initialPath)},[initialPath]);
  useEffect(()=>{
    recoverPending().then(()=>setRecovered(true)).catch(reason=>setError((reason as Error).message));
  },[]);
  useEffect(()=>{
    if(!recovered)return;
    const controller=new AbortController();
    json<Stats>('/api/stats').then(setStats).catch(reason=>{if(!controller.signal.aborted)setError((reason as Error).message)});
    // The badge is read again whenever a page says the Bin may have changed.
    // A burst of saves collapses into one read, and a failed read keeps the
    // last count rather than blanking the page.
    let timer=0;
    const reread=()=>{
      clearTimeout(timer);
      timer=window.setTimeout(()=>{json<Stats>('/api/stats').then(next=>setStats(current=>current&&{...current,bin:next.bin,marked:next.marked})).catch(()=>{})},250);
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
      if(path==='/year')return {route,content:<Year {...await json<YearData>('/api/year')}/>} as PageState;
      // Coverage against the files that could possibly be duplicates is what makes
      // an empty result readable: no groups found is a different statement from no
      // groups because nothing was ever hashed.
      if(path==='/duplicates')return {route,content:<Duplicates report={await json<DuplicateReport>('/api/duplicate-report?limit=1000')}/>} as PageState;
      if(path==='/upgrades')return {route,content:<Upgrades initial={await json<UpgradePage>('/api/upgrades')}/>} as PageState;
      if(path==='/log')return {route,content:<Log initial={await json<HistoryEvent[]>('/api/log?limit=200')}/>} as PageState;
      if(path==='/bin')return {route,content:<Bin onCount={count=>setStats(current=>current&&{...current,bin:count})}/>} as PageState;
      if(path==='/settings')return {route,content:<Settings stats={await json<Stats>('/api/stats')}/>} as PageState;
      if(path==='/shadows')return {route,content:<Shadows groups={await json<Parameters<typeof Shadows>[0]['groups']>('/api/shadows')}/>} as PageState;
      if(path==='/social'){
        const params=new URLSearchParams(location.search),band=params.get('band')||'',from=Math.max(0,Number.parseInt(params.get('from')||'0',10)||0);
        return {route,content:<Social page={await json<SocialPage>(`/api/social?band=${encodeURIComponent(band)}&from=${from}`)} band={band} from={from}/>} as PageState;
      }
      if(path==='/screenshots'){
        const params=new URLSearchParams(location.search),filter=params.get('show')||'',from=Math.max(0,Number.parseInt(params.get('from')||'0',10)||0);
        return {route,content:<Screenshots page={await json<ScreenshotPage>(`/api/screenshots?kind=${encodeURIComponent(filter)}&from=${from}`)} filter={filter} from={from}/>} as PageState;
      }
      throw new Error('This legacy workflow has not been connected yet.');
    }
    load().then(result=>{if(active)setPage(result)}).catch(reason=>{if(active)setError((reason as Error).message)});
    return()=>{active=false};
  },[path,recovered]);
  return <Layout route={page.route} binFiles={stats?.bin??stats?.marked??0}>{error?<p className="note warn" role="alert">{error} <button className="btn small" onClick={()=>location.reload()}>Retry</button></p>:page.content}</Layout>;
}
