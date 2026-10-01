import {useState,type FormEvent} from 'react';
import {Media} from '../Media';
import type {Page} from '../api';
import {navigate} from './router';
import {dayOfPath} from './goto';

type SavedView={name:string;query:string};
const savedKey='cull.library.views';
function readViews():SavedView[]{try{const value:unknown=JSON.parse(localStorage.getItem(savedKey)??'[]');return Array.isArray(value)?value.filter((v):v is SavedView=>typeof v?.name==='string'&&typeof v?.query==='string').slice(0,8):[]}catch{return []}}

export function Library({initial}:{initial:Page}){
  const params=new URLSearchParams(location.search);
  const [views,setViews]=useState(readViews);
  const [name,setName]=useState('');
  const [message,setMessage]=useState('');
  function search(event:FormEvent<HTMLFormElement>){event.preventDefault();const data=new FormData(event.currentTarget);const query=new URLSearchParams();for(const [key,value] of data)if(typeof value==='string'&&value)query.set(key,value);navigate(`/library?${query}`)}
  function save(){if(!name.trim())return;const query=new URLSearchParams(location.search);query.delete('after');const next=[...views.filter(v=>v.name!==name.trim()),{name:name.trim(),query:query.toString()}].slice(-8);try{localStorage.setItem(savedKey,JSON.stringify(next));setViews(next);setName('');setMessage('View saved in this browser.')}catch{setMessage('This browser could not save the view.')}}
  const next=new URLSearchParams(location.search);next.set('after',initial.next);
  return <section className="libraryview"><h1>Library</h1>
    <p className="hint">Search filenames, folders, or camera models across the archive. Camera matches appear as metadata is indexed.</p>
    <form className="libraryfilters" onSubmit={search}>
      <label>Find<input name="q" type="search" defaultValue={params.get('q')??''} placeholder="Filename, folder or camera"/></label>
      <label>Kind<select name="kind" defaultValue={params.get('kind')??''}><option value="">All media</option><option value="image">Photos</option><option value="raw">RAW</option><option value="video">Videos</option></select></label>
      <label>Decision<select name="status" defaultValue={params.get('status')??''}><option value="">Any decision</option><option value="unreviewed">Unreviewed</option><option value="keep">Kept</option><option value="later">Later</option></select></label>
      <label>From<input name="from" type="date" defaultValue={params.get('from')??''}/></label><label>To<input name="to" type="date" defaultValue={params.get('to')??''}/></label>
      <label className="librarycheck"><input name="favourite" type="checkbox" value="1" defaultChecked={params.get('favourite')==='1'}/>Favourites</label>
      <button className="btn primary" type="submit">Search</button>
    </form>
    <nav className="queuepages" aria-label="Library views"><a className="btn" href="/library?status=unreviewed">Unreviewed</a><a className="btn" href="/library?favourite=1">Favourites</a><a className="btn" href="/library?kind=video&status=unreviewed">Unreviewed videos</a>{views.map(view=><a className="btn" href={`/library?${view.query}`} key={view.name}>{view.name}</a>)}</nav>
    <form className="saveview" onSubmit={event=>{event.preventDefault();save()}}><label>Save this view<input value={name} onChange={event=>setName(event.target.value)} maxLength={40} placeholder="View name"/></label><button className="btn" disabled={!name.trim()}>Save view</button></form>
    {message&&<p role="status">{message}</p>}
    <p className="ysum">{initial.assets.length} files on this page. Open a photo to review it in its day.</p>
    {initial.assets.length===0?<p className="note">No files match these filters. <a href="/library">Clear filters</a></p>:<div className="librarygrid">{initial.assets.map(asset=>{
      const day=dayOfPath(asset.path)??`/day/${new Date(asset.capturedAt*1000).toISOString().slice(0,10)}`;
      return <a className="librarytile" key={asset.id} href={`${day}/photo/${asset.id}`}><Media asset={asset}/><span>{asset.path.split('/').pop()}</span><small>{asset.status}{asset.favourite?' · Favourite':''}</small></a>;
    })}</div>}
    <nav className="queuepages" aria-label="Search pages">{params.has('after')&&<a className="btn" href={`/library?${(()=>{const first=new URLSearchParams(location.search);first.delete('after');return first})()}`}>First page</a>}{initial.next&&<a className="btn primary" href={`/library?${next}`}>Next 50 files</a>}</nav>
  </section>;
}
