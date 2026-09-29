import {useEffect,useState,type ReactNode} from 'react';
import {Icon} from '../Icon';
import {Kbd,MAC,tipProps} from './keys';
import type {LegacyRoute} from './Layout';

// Each page opens with a short guide the first time it is visited: what the
// page is for and how to work it, keys included. Hiding it is remembered in
// this browser, and the question mark in the top bar brings it back.

const HIDDEN='cull-guides-hidden';
const CHANGED='cull:guides-changed';

const words:Record<string,string>={Mod:MAC?'Command':'Control',ArrowLeft:'left arrow',ArrowRight:'right arrow','?':'question mark','/':'slash'};

/** A key as the guide writes it: drawn as chips, read out as words. */
function K({keys}:{keys:string}){
  return <span className="guidekey"><Kbd keys={keys}/><span className="sronly">{keys.split('+').map(part=>words[part]??part).join(' ')}</span></span>;
}

type Guide={title:string;points:ReactNode[]};

const guides:Partial<Record<LegacyRoute,Guide>>={
  today:{title:'How a day works',points:[
    <>Every photo taken on this date, in every year, grouped by year. Click a photo, or press <K keys="Enter"/>, to open it.</>,
    <>In a photo, <K keys="K"/> keeps, <K keys="X"/> removes and <K keys="F"/> favourites. Keep and remove move on to the next photo; <K keys="ArrowLeft"/> and <K keys="ArrowRight"/> step through. <K keys="?"/> lists every key.</>,
    <>On the grid, the round tick in a photo’s corner selects it, and Shift-click selects everything up to it. The bar at the top then acts on the whole selection.</>,
    <><K keys="Mod+Z"/> undoes and <K keys="Mod+U"/> redoes. Removed photos wait in the Bin: nothing is deleted from here.</>,
    <>When the day is done, mark it reviewed with <K keys="Shift+R"/>. Press <K keys="/"/> and type a date, such as 14 Aug 2019, to go to another.</>,
  ]},
  year:{title:'How the calendar works',points:[
    <>Each square is a date. Its colour says how many photos taken on it, across the years, still wait for review.</>,
    <>Click a date to review it. Reviewed dates are marked, and the Reviewed meter in the sidebar counts them.</>,
  ]},
  dupes:{title:'How duplicates work',points:[
    <>Each group holds files with exactly the same bytes, in different places. Two photos that only look alike are not here: open one and press <K keys="C"/> to compare similar photos.</>,
    <><b>Keep</b> chooses which copy stays in each group: the plainest name, the oldest or the newest. Click another copy to keep that one instead.</>,
    <><b>Merge</b> moves the other copies to the Bin, where they can be restored. <b>Skip</b> leaves a group alone; <K keys="Shift+M"/> merges every group not skipped.</>,
  ]},
  upgrades:{title:'How upgrades work',points:[
    <>Each pair is a photo in the library beside a copy of it from Google Takeout with more pixels.</>,
    <><b>Add the higher-resolution copy</b> saves it next to the original with “(hi-res)” in its name. Nothing is replaced.</>,
  ]},
  shadows:{title:'What shadowed files are',points:[
    <>Files whose paths collide, so one can hide another: the same name in a different case, or a folder hidden behind a share. This page only shows them; nothing here changes a file.</>,
  ]},
  shots:{title:'How screenshots work',points:[
    <>Screenshots and screen recordings, kept apart from the photos so they do not crowd a day. Filter by not reviewed, reviewed, stills or recordings.</>,
    <>Select with the tick, then <K keys="K"/> keeps, <K keys="X"/> moves to the Bin, <K keys="C"/> copies into the library and <K keys="U"/> marks not reviewed.</>,
    <><K keys="Mod+Z"/> undoes, as on a day.</>,
  ]},
  social:{title:'How saved-from-social works',points:[
    <>Videos that look saved from social apps rather than filmed, marked likely or not sure.</>,
    <>Select with the tick, then <K keys="K"/> keeps them or <K keys="X"/> moves them to the Bin. <K keys="Mod+Z"/> undoes.</>,
  ]},
  photos:{title:'How Apple Photos works',points:[
    <>Carries what you remove and favourite here across to Photos on your Mac, through the small Cull Sync app.</>,
    <><b>Check Photos</b> lists what would change. Untick anything to leave it alone in Photos.</>,
    <><b>Apply in Photos</b> (<K keys="Shift+A"/>) sets favourites first. Photos asks on the Mac before deleting, and deleted photos go to its Recently Deleted.</>,
  ]},
  google:{title:'How Google Photos works',points:[
    <>Reads the Google Takeout exports in your Takeout folder and sorts every photo by whether the library already has it.</>,
    <>In <b>Not in the library</b>, select photos and press <K keys="A"/> to add them under the day each was taken, or <K keys="S"/> to skip them. <K keys="U"/> offers skipped ones again.</>,
    <><b>Different copies</b> and <b>Unsure</b> deserve a look before adding. A photo is never added twice, and nothing in the library is overwritten.</>,
  ]},
  log:{title:'How the log works',points:[
    <>Every choice you saved, newest first, grouped by the day you made it.</>,
    <><b>Undo</b> on a photo saves the opposite choice. A photo now in the Bin links there.</>,
  ]},
  bin:{title:'How the Bin works',points:[
    <>Everything you removed waits here, and can go back where it came from: select it and press <K keys="R"/> to restore.</>,
    <><K keys="Delete"/> deletes the selected files and <K keys="Shift+Delete"/> empties the Bin. Both ask first.</>,
    <>Deleted files are kept for the number of days set in Settings, and can be restored from the Log until then.</>,
  ]},
  settings:{title:'What Settings holds',points:[
    <><b>Folders</b> shows where the library and the Import folder are and what the Import folder last held. <b>Change in setup</b> reopens the setup.</>,
    <><b>Bin</b> sets how long deleted files are kept before they are gone for good.</>,
    <><b>Appearance</b> chooses day, night or automatic, by the time of day.</>,
  ]},
  addons:{title:'How addons work',points:[
    <>Turn Cull’s own features on or off, such as Screenshots or Google Photos, and addons you added yourself.</>,
    <>Turning one on first shows what it will be allowed to do. <a href="/developers">Developers</a> has everything needed to build your own.</>,
  ]},
  developers:{title:'How the API works',points:[
    <>Everything the app does goes through this API, and addons use it too. Each route says which permission it needs.</>,
    <><b>Try it</b> sends read-only requests from this page. <a href="/api/openapi.json">openapi.json</a> describes it all for other tools.</>,
  ]},
};

function readHidden():Set<string>{
  try{
    const value=JSON.parse(localStorage.getItem(HIDDEN)||'[]') as unknown;
    return new Set(Array.isArray(value)?value.filter(item=>typeof item==='string'):[]);
  }catch{return new Set()}
}

function writeHidden(hidden:Set<string>){
  try{localStorage.setItem(HIDDEN,JSON.stringify([...hidden]))}catch{/* kept for this visit only */}
  window.dispatchEvent(new Event(CHANGED));
}

/** Shows every page's guide again, and says how many were hidden. */
export function showAllGuides(){
  const count=readHidden().size;
  writeHidden(new Set());
  return count;
}

/** How many pages' guides are hidden. */
export function hiddenGuides(){return readHidden().size}

function useHidden(){
  const [hidden,setHidden]=useState(readHidden);
  useEffect(()=>{
    const again=()=>setHidden(readHidden());
    window.addEventListener(CHANGED,again);
    window.addEventListener('storage',again);
    return()=>{window.removeEventListener(CHANGED,again);window.removeEventListener('storage',again)};
  },[]);
  return hidden;
}

function setShown(route:LegacyRoute,shown:boolean){
  const hidden=readHidden();
  if(shown)hidden.delete(route);else hidden.add(route);
  writeHidden(hidden);
}

/** The guide for a page, at the top of it, unless it has been hidden. */
export function PageGuide({route}:{route:LegacyRoute}){
  const hidden=useHidden();
  const guide=guides[route];
  if(!guide||hidden.has(route))return null;
  return <aside className="guide" aria-label={guide.title}>
    <div className="guidehead">
      <Icon name="info"/>
      <h2>{guide.title}</h2>
      <button type="button" className="btn small" onClick={()=>setShown(route,false)}>Got it</button>
    </div>
    <ul>{guide.points.map((point,index)=><li key={index}>{point}</li>)}</ul>
  </aside>;
}

/** The top bar's button that brings a hidden guide back, or hides it. */
export function GuideButton({route}:{route:LegacyRoute}){
  const hidden=useHidden();
  if(!guides[route])return null;
  const shown=!hidden.has(route);
  return <button type="button" className={`iconbtn guidebtn${shown?' on':''}`} aria-pressed={shown} aria-label="How this page works" {...tipProps(shown?'Hide how this page works':'How this page works')} onClick={()=>{setShown(route,!shown);if(!shown)window.scrollTo(0,0)}}>
    <span className="guidemark" aria-hidden="true">?</span>
  </button>;
}
