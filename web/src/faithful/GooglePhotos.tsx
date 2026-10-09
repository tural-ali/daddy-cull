import {useCallback,useEffect,useRef,useState} from 'react';
import {FilePreview} from '../Media';
import {failure} from '../api';
import {Icon} from '../Icon';
import {Busy} from '../Busy';
import {Pick,RowTile,Rows,useSelectionBar,usePicks,type SelectionAction} from './selection';
import {MoreMarker,useMoreOnScroll} from './more';
import {usePhotoURL} from './photoURL';
import {Lightbox,type LightboxItem} from './Lightbox';
import {TASK_FINISHED,queueGooglePhotos,type Task} from './taskQueue';
import {Kbd,keyProps,touch} from './keys';

type Outcome='pending'|'missing'|'alternative'|'uncertain'|'represented'|'removed';
type Item={id:number;name:string;kind:string;size:number;taken:string;takenFrom?:string;outcome:Outcome;reason:string;
  match?:{assetId:number;path:string;size:number};state:'waiting'|'added'|'skipped';addedAs?:string;addedAt?:string;
  favourite:boolean;description?:string;people:string[];archive:string;copies:number};
type Archive={name:string;kind:'zip'|'folder'|'unsupported';size:number;media:number;scannedAt:string;problem?:string};
type Counts={missing:number;alternative:number;uncertain:number;represented:number;removed:number;added:number;skipped:number;checking:number};
type TabID='missing'|'alternative'|'uncertain'|'represented'|'removed'|'added'|'skipped';
export type GooglePhotosPage={inbox:boolean;scanning:boolean;scannedAt:string;problem?:string;archives:Archive[];counts:Counts;tab:TabID;items:Item[];next:number};

// The tabs follow what checking a photo against the library came to. The
// first two hold what can be added; the rest are there to be trusted, so a
// photo Cull holds back is never simply missing from the page.
const TABS:{id:TabID;label:string;empty:string;about:string}[]=[
  {id:'missing',label:'Not in the library',empty:'Every photo in the inbox is already in the library, or held back under another tab.',about:'The library has no copy of these. Adding one copies it under the day it was taken.'},
  {id:'alternative',label:'Different copies',empty:'No photo here differs from the library’s copy.',about:'The library holds a photo of the same name from the same day, but not these bytes: often Google kept a smaller or edited copy. Adding puts this one beside the library’s, named with (Google Photos).'},
  {id:'uncertain',label:'Unsure',empty:'Cull could place every photo.',about:'Cull cannot tell where these belong: Google did not say when they were taken, or a file like them could not be compared. They cannot be added from here.'},
  {id:'represented',label:'Already in the library',empty:'Nothing in the inbox is in the library yet.',about:'The library already holds these bytes, under this name or another.'},
  {id:'removed',label:'Removed in Cull',empty:'Nothing here was removed in Cull.',about:'You removed these, or the library’s copy of them, in Cull, so they are not offered again.'},
  {id:'added',label:'Added',empty:'Nothing has been added from Google Photos yet.',about:'Added to the library from here, newest first.'},
  {id:'skipped',label:'Skipped',empty:'Nothing has been skipped.',about:'Set aside, so they are not offered. Take them back to have them offered again.'},
];
const addable=new Set<TabID>(['missing','alternative']);
const skippable=new Set<TabID>(['missing','alternative','uncertain']);

function bytes(value:number){return value<1024**2?`${Math.max(1,Math.round(value/1024))} KB`:value<1024**3?`${(value/1024**2).toFixed(1)} MB`:`${(value/1024**3).toFixed(1)} GB`}
function plural(count:number,one:string,many=`${one}s`){return `${count.toLocaleString()} ${count===1?one:many}`}
function countOf(counts:Counts,tab:TabID){return counts[tab]}
function takenLabel(item:Item){
  if(!item.taken)return 'date unknown';
  const when=new Date(`${item.taken}Z`);
  return when.toLocaleDateString('en-GB',{day:'numeric',month:'short',year:'numeric',timeZone:'UTC'});
}
function ago(stamp:string){
  if(!stamp)return '';
  const minutes=Math.round((Date.now()-Date.parse(stamp))/60000);
  if(minutes<1)return 'just now';
  if(minutes<60)return `${plural(minutes,'minute')} ago`;
  return new Date(stamp).toLocaleTimeString('en-GB',{hour:'2-digit',minute:'2-digit'});
}
function read(tab:TabID,from:number){return `/api/google-photos?tab=${tab}&from=${from}`}
async function post<T>(url:string,body:unknown,fallback:string):Promise<T>{
  const response=await fetch(url,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});
  if(!response.ok)throw new Error(await failure(response,fallback));
  return response.json() as Promise<T>;
}

/** How to get Google Photos out of Google. Open until the first export
 * arrives, then folded away under its summary. */
function Steps({open,inbox}:{open:boolean;inbox:boolean}){
  return <details className="gsteps" open={open}>
    <summary>How to bring your Google Photos here</summary>
    <p className="dim">Google no longer lets an app download a whole Google Photos library, so the photos come out through Google Takeout, which packs them into .zip files. Cull reads the files where they are and never changes them.</p>
    <ol>
      <li><b>Open <a href="https://takeout.google.com" target="_blank" rel="noreferrer">takeout.google.com</a></b>, signed in to the Google account that has the photos. Click <b>Deselect all</b>, then tick only <b>Google Photos</b> and click <b>Next step</b>.</li>
      <li><b>Choose how often.</b> <i>Export once</i> is enough for a library you are leaving; <i>Export every 2 months for 1 year</i> keeps bringing in what the phones upload.</li>
      <li><b>Choose .zip and 50 GB.</b> The largest size keeps the parts few. Google emails when the export is ready, which can take a day or more.</li>
      <li><b>Download every part into the inbox folder</b>{inbox?'':', once one is set up'}, as they are. Do not unpack them; a folder already unpacked works too. Cull reads the inbox every five minutes.</li>
      <li><b>Choose what to add.</b> Photos the library has no copy of wait under <i>Not in the library</i>. Select them and press <b>Add to the library</b>; each goes under the day it was taken.</li>
      <li><b>Delete the exports</b> from the inbox once what you want is added. The photos already added stay in the library.</li>
    </ol>
  </details>;
}

export function GooglePhotos({initial}:{initial:GooglePhotosPage}){
  const [page,setPage]=useState(initial);
  const [tab,setTab]=useState<TabID>(initial.tab);
  const [busy,setBusy]=useState(false);
  const [message,setMessage]=useState('');
  const [error,setError]=useState('');
  const version=useRef(0);
  const {items,counts}=page;
  const picks=usePicks(items,item=>item.id);
  const photo=usePhotoURL(id=>initial.items.some(item=>String(item.id)===id));

  const load=useCallback(async(next:TabID,quiet=false)=>{
    const asked=++version.current;
    try{
      const response=await fetch(read(next,0));
      if(!response.ok)throw new Error('The photos could not be read. Try again.');
      const fresh:GooglePhotosPage=await response.json();
      if(asked!==version.current)return;
      setPage(fresh);setTab(next);
      if(!quiet)window.scrollTo({top:0});
    }catch(reason){if(asked===version.current)setError((reason as Error).message)}
  },[]);

  function choose(next:TabID){
    if(next===tab)return;
    const address=new URL(location.href);
    address.search=next==='missing'?'':new URLSearchParams({tab:next}).toString();
    history.replaceState(history.state,'',address);
    picks.clear();setMessage('');setError('');
    void load(next);
  }

  const more=useMoreOnScroll(tab,page.next>0,items.length,async()=>{
    const asked=version.current;
    const response=await fetch(read(tab,items.length));
    if(!response.ok)throw new Error('more');
    const next:GooglePhotosPage=await response.json();
    if(asked!==version.current)return;
    setPage(value=>{
      const have=new Set(value.items.map(item=>item.id));
      return {...next,items:[...value.items,...next.items.filter(item=>!have.has(item.id))]};
    });
  });

  // While the inbox is being read the counts are followed, and the tab is
  // read again when it is done.
  useEffect(()=>{
    if(!page.scanning)return;
    const timer=setTimeout(()=>void load(tab,true),2000);
    return()=>clearTimeout(timer);
  },[page,tab,load]);

  // Photos a task could not add come back to the page, which says so.
  useEffect(()=>{
    function finished(event:Event){
      const task=(event as CustomEvent<Task>).detail;
      if(task.kind!=='google-photos.add')return;
      void load(tab,true).then(()=>{
        if(task.failed>0)setError(`${plural(task.failed,'photo was','photos were')} not added and ${task.failed===1?'is':'are'} back on the page. Tasks says why.`);
        else setMessage(`Added ${plural(task.done,'photo')} to the library. ${task.done===1?'It is':'They are'} under Added.`);
      });
    }
    window.addEventListener(TASK_FINISHED,finished);
    return()=>window.removeEventListener(TASK_FINISHED,finished);
  });

  async function scan(){
    setBusy(true);setError('');setMessage('');
    try{setPage(value=>({...value,scanning:true}));await post('/api/google-photos/scan',{},'The inbox could not be read again.')}
    catch(reason){setError((reason as Error).message)}finally{setBusy(false)}
  }

  /** Takes what left the tab off it, and out of its count. */
  function drop(ids:Set<number>,into?:keyof Counts){
    version.current++;
    setPage(value=>({...value,items:value.items.filter(item=>!ids.has(item.id)),
      counts:{...value.counts,[tab]:value.counts[tab]-ids.size,...(into?{[into]:value.counts[into]+ids.size}:{})}}));
    picks.setPicked(previous=>new Set([...previous].filter(id=>!ids.has(id))));
  }

  async function add(chosen:Item[]){
    if(chosen.length===0||busy)return;
    setBusy(true);setError('');setMessage('');
    try{
      await queueGooglePhotos(chosen.map(item=>item.id));
      drop(new Set(chosen.map(item=>item.id)));
      setMessage(chosen.length===1?`Adding ${chosen[0].name} to the library.`:`Adding ${plural(chosen.length,'photo')} to the library. It carries on in the background under Tasks.`);
    }catch(reason){setError((reason as Error).message)}finally{setBusy(false)}
  }

  async function skip(chosen:Item[],set:boolean){
    if(chosen.length===0||busy)return;
    setBusy(true);setError('');setMessage('');
    try{
      const result=await post<{changed:number}>('/api/google-photos/skip',{ids:chosen.map(item=>item.id),skip:set},set?'The photos could not be skipped.':'The photos could not be taken back.');
      drop(new Set(chosen.map(item=>item.id)),set?'skipped':undefined);
      if(!set)void load(tab,true);
      setMessage(set?`Skipped ${plural(result.changed,'photo')}. ${result.changed===1?'It is':'They are'} under Skipped, to take back at any time.`:`${plural(result.changed,'photo is','photos are')} offered again.`);
    }catch(reason){setError((reason as Error).message)}finally{setBusy(false)}
  }

  const chosen=items.filter(item=>picks.picked.has(item.id));
  const allShown=items.length>0&&items.every(item=>picks.picked.has(item.id));
  const actions:SelectionAction[]=[
    {label:allShown?'Deselect all':`Select all ${items.length} shown`,icon:'select_all',keys:'Mod+A',onClick:()=>allShown?picks.clear():picks.all()},
    ...(addable.has(tab)?[{label:'Add to the library',icon:'library_add',keys:'A',onClick:()=>void add(chosen)}] as SelectionAction[]:[]),
    ...(skippable.has(tab)?[{label:'Skip',icon:'visibility_off',keys:'S',onClick:()=>void skip(chosen,true)}] as SelectionAction[]:[]),
    ...(tab==='skipped'?[{label:'Offer again',icon:'undo',keys:'U',onClick:()=>void skip(chosen,false)}] as SelectionAction[]:[]),
  ];
  useSelectionBar({count:picks.picked.size,busy,clear:picks.clear,actions});

  const about=TABS.find(entry=>entry.id===tab)!;
  const waiting=counts.missing+counts.alternative;
  const media=page.archives.reduce((sum,archive)=>sum+archive.media,0);
  const exportsSize=page.archives.reduce((sum,archive)=>sum+archive.size,0);
  const previews:LightboxItem[]=items.map(item=>({key:String(item.id),base:`/api/google-photos/media/${item.id}`,name:item.name,kind:item.kind,detail:[takenLabel(item),bytes(item.size)].join(' · ')}));

  return <>
    <section className="dupehead">
      <h1>Google Photos</h1>
      {page.archives.length>0&&<p className="ysum"><b>{plural(page.archives.length,'export')}</b> in the inbox · <b>{bytes(exportsSize)}</b> · <b>{media.toLocaleString()}</b> photos and videos · <b>{waiting.toLocaleString()}</b> can be added</p>}
      <p className="gscan">
        {page.scanning?<Busy label={counts.checking>0?`Checking ${plural(counts.checking,'photo')} against the library…`:'Reading the inbox…'} state="searching"/>
          :<span className="dim">{page.scannedAt?`Read ${ago(page.scannedAt)}. Read again every five minutes.`:'Not read yet.'}</span>}
        {page.inbox&&<button type="button" className="btn small" disabled={busy||page.scanning} onClick={()=>void scan()}><Icon name="refresh"/>Read the inbox now</button>}
      </p>
    </section>
    {!page.inbox&&<p className="note warn">Cull was started without a Takeout inbox, so there is nowhere to drop exports yet. Give it a folder with <code>-takeout-inbox</code>, mounted read-only into both Cull and its writer, then come back here.</p>}
    {page.problem&&<p className="note warn" role="alert">{page.problem}</p>}
    <Steps open={page.archives.length===0} inbox={page.inbox}/>
    {page.archives.length>0&&<details className="gexports">
      <summary>{plural(page.archives.length,'export')} in the inbox{page.archives.some(archive=>archive.problem)?', some with a problem':''}</summary>
      <ul>{page.archives.map(archive=><li key={archive.name}>
        <span className="mono">{archive.name}</span>
        <span className="dim">{archive.kind==='folder'?'folder':archive.kind==='zip'?'zip':'cannot be read'} · {bytes(archive.size)} · {plural(archive.media,'photo')}</span>
        {archive.problem&&<span className="gproblem">{archive.problem}</span>}
      </li>)}</ul>
    </details>}
    {page.archives.length>0&&<>
      <nav className="gtabs" aria-label="What Cull found">
        {TABS.map(entry=>{
          const n=countOf(counts,entry.id);
          return <button type="button" key={entry.id} className={entry.id===tab?'on':''} aria-pressed={entry.id===tab} onClick={()=>choose(entry.id)}>
            {entry.label}<span className="n">{n.toLocaleString()}</span>
          </button>;
        })}
      </nav>
      <p className="hint">{about.about}{addable.has(tab)&&items.length>0?touch?' Tap a photo to look at it; hold one to select it.':' Click a photo to look at it; tick the circle to select, Shift-click to select a run.':''}</p>
    </>}
    {message&&<p className="flash" role="status">{message}</p>}
    {error&&<p className="note warn" role="alert">{error}</p>}
    {page.archives.length>0&&(items.length===0
      ? <p className="note">{about.empty}</p>
      : <Rows className="gphotos">{items.map((item,index)=>{
          const picked=picks.picked.has(item.id);
          const detail=[takenLabel(item),bytes(item.size)].join(' · ');
          return <RowTile className={picked?'picked':''} key={item.id} data-item={item.id} title={`${item.name} · ${detail}\n${item.reason}`}
            onClick={event=>{if(!picks.tap(index,event.shiftKey))photo.show(String(item.id))}}>
            <FilePreview base={`/api/google-photos/media/${item.id}`} name={item.name} kind={item.kind}/>
            <Pick checked={picked} label={`Select ${item.name}, ${detail}`} onToggle={extend=>picks.toggle(index,extend)}/>
            <div className="bdg">
              {!item.taken&&<span className="b warn">undated</span>}
              {item.outcome==='alternative'&&tab!=='alternative'&&<span className="b">different copy</span>}
            </div>
            {item.favourite&&<span className="gfav" aria-label="Starred in Google Photos"><Icon name="favorite" filled/></span>}
          </RowTile>;
        })}</Rows>)}
    {photo.open!==null&&previews.length>0&&<Lightbox items={previews} initialKey={photo.open} onClose={photo.close} onMove={photo.moved} renderActions={current=>{
      const item=items.find(entry=>String(entry.id)===current.key);
      if(!item)return null;
      return <>
        <span className="greason">{item.reason}{item.match?<> <a href={`/api/media/${item.match.assetId}/original`} target="_blank" rel="noreferrer">Open the library’s copy</a></>:null}</span>
        {addable.has(tab)&&<button type="button" className="rvbtn primary" disabled={busy} {...keyProps('A')} onClick={()=>void add([item])}>Add to the library<Kbd keys="A"/></button>}
        {skippable.has(tab)&&<button type="button" className="rvbtn" disabled={busy} {...keyProps('S')} onClick={()=>void skip([item],true)}>Skip<Kbd keys="S"/></button>}
      </>;
    }}/>}
    <MoreMarker state={more} what="photos"/>
  </>;
}
