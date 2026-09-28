import {useEffect,useRef} from 'react';
import {Icon,addonIcon} from '../Icon';
import {themeEvent} from '../theme';
import {isAppPath,navigate} from './router';
import type {Addon,AddonPage} from './addonList';

// A page an addon of your own serves, shown inside Cull's frame. The addon
// runs apart from Cull, at its own address, so the page is sandboxed there
// and talks to Cull only through messages: Cull says which theme it is in,
// and the page may ask Cull to go to one of its own pages.

/** The colours a page needs to look like part of Cull, from the theme now. */
function palette(){
  const style=getComputedStyle(document.documentElement);
  const read=(name:string)=>style.getPropertyValue(`--${name}`).trim();
  return {background:read('bg'),surface:read('bg-2'),text:read('text'),muted:read('muted'),line:read('line'),accent:read('accent'),onAccent:read('on-accent'),warn:read('warn'),bad:read('bad')};
}
function theme(){return document.documentElement.dataset.theme==='day'?'light':'dark'}

export function AddonFrame({addon,page}:{addon:Addon;page:AddonPage}){
  const frame=useRef<HTMLIFrameElement>(null);
  const url=new URL(page.url??'about:blank');
  const origin=url.origin;
  // A page served from Cull's own address would be Cull, so it is never
  // given the same origin, which would let it out of the sandbox.
  const sandbox=`allow-scripts allow-forms allow-popups allow-downloads${origin===location.origin?'':' allow-same-origin'}`;
  useEffect(()=>{document.title=`${page.label} · Daddy, Cull!`},[page.label]);
  useEffect(()=>{
    const say=(type:'cull:hello'|'cull:theme')=>frame.current?.contentWindow?.postMessage({type,version:1,addon:addon.id,page:page.id,theme:theme(),colors:palette()},origin);
    const element=frame.current;
    const loaded=()=>say('cull:hello');
    const changed=()=>say('cull:theme');
    const heard=(event:MessageEvent)=>{
      if(event.source!==frame.current?.contentWindow||event.origin!==origin)return;
      const data=event.data as {type?:unknown;path?:unknown}|null;
      if(data?.type!=='cull:navigate'||typeof data.path!=='string'||!data.path.startsWith('/'))return;
      // Read as an address, so //elsewhere/bin is seen for what it is.
      const to=new URL(data.path,location.origin);
      if(to.origin===location.origin&&isAppPath(to.pathname))navigate(to.pathname+to.search+to.hash);
    };
    element?.addEventListener('load',loaded);
    window.addEventListener(themeEvent,changed);
    window.addEventListener('message',heard);
    return()=>{element?.removeEventListener('load',loaded);window.removeEventListener(themeEvent,changed);window.removeEventListener('message',heard)};
  },[addon.id,page.id,origin]);
  return <section className="addonframe">
    <header className="framehead">
      <span className="addontile small"><Icon name={addonIcon(page.icon||addon.icon)} filled/></span>
      <h1>{page.label}<span className="framefrom">{addon.name}</span></h1>
      <a className="textbtn framelink" href={url.href} target="_blank" rel="noreferrer noopener" aria-label="Open on its own" title="Open on its own"><Icon name="open_in_new"/><span className="framelinklabel">Open on its own</span></a>
    </header>
    <iframe ref={frame} src={url.href} title={`${page.label}, from ${addon.name}`} sandbox={sandbox} referrerPolicy="no-referrer" allow="clipboard-write"/>
  </section>;
}

/** An addon's page that cannot be shown: the addon is off, gone, or has no
 * such page. */
export function AddonMissing({addon,label}:{addon?:Addon;label:string}){
  return <section className="addonframe missing">
    <div className="addonempty">
      <span className="addontile"><Icon name={addonIcon(addon?.icon)}/></span>
      <p>{addon?addon.problem?<>{addon.name} cannot be loaded. {addon.problem}</>:<>{label===addon.name?label:<>{label} is part of {addon.name}, which</>} is turned off. <a href="/addons">Turn it on in Addons</a> to see it.</>
        :<>No addon has this page. It may have been taken out of the addons folder. <a href="/addons">See your addons</a></>}</p>
    </div>
  </section>;
}
