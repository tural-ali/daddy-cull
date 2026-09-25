import type {ReactNode} from 'react';
import {Logo} from '../Logo';

export type LegacyRoute='today'|'year'|'dupes'|'upgrades'|'shadows'|'shots'|'social'|'log'|'bin'|'settings';
const navigation: {href:string;route:LegacyRoute;label:string}[]=[
  {href:'/',route:'today',label:'Today'},
  {href:'/year',route:'year',label:'Year'},
  {href:'/duplicates',route:'dupes',label:'Duplicates'},
  {href:'/shadows',route:'shadows',label:'Shadowed'},
  {href:'/screenshots',route:'shots',label:'Screenshots'},
  {href:'/social',route:'social',label:'Saved from social'},
  {href:'/log',route:'log',label:'Log'},
  {href:'/bin',route:'bin',label:'Bin'},
];

/** Direct counterpart of templates/layout.php; navigation is not redesigned. */
export function Layout({route,binFiles,flash,children}:{route:LegacyRoute;binFiles:number;flash?:string;children:ReactNode}){
  return <>
    <header className="bar">
      <a className="brand" href="/" title="Today"><Logo/></a>
      <nav aria-label="Main navigation">
        {navigation.map(item=><a key={item.route} href={item.href} className={route===item.route?'on':undefined} aria-current={route===item.route?'page':undefined}>
          {item.label}{item.route==='bin'&&binFiles>0&&<> <span className="pill">{binFiles.toLocaleString()}</span></>}
        </a>)}
      </nav>
      <a className={`cog${route==='settings'?' on':''}`} href="/settings" title="Indexing and maintenance" aria-current={route==='settings'?'page':undefined}>Settings</a>
    </header>
    {flash&&<p className="flash" role="status">{flash}</p>}
    <main className={route==='today'||route==='year'?'wide':undefined}>{children}</main>
  </>;
}
