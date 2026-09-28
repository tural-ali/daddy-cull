import {useEffect,useRef,useState} from 'react';
import {Icon,addonIcon} from '../Icon';
import {Busy} from '../Busy';
import {Kbd,keyProps} from './keys';
import {readReference,type Permission} from './reference';
import {setAddon,useAddons,type Addon} from './addonList';

// One place to turn off what is not wanted. Each addon says what it is for,
// whether it has what it needs, and what it does while it is on; turning one
// off takes its page from the sidebar and stops its work, and undoes nothing.

const stateLabels={ready:'Ready',setup:'Needs setting up',problem:'Problem'} as const;

function plural(count:number,word:string){return `${count.toLocaleString()} ${word}${count===1?'':'s'}`}
function slug(text:string){return text.toLowerCase().replace(/[^a-z0-9]+/g,'-').replace(/^-|-$/g,'')}

/** What each permission lets an addon do, from the API reference. */
function usePermissions(wanted:boolean){
  const [permissions,setPermissions]=useState<Permission[]|null>(null);
  useEffect(()=>{
    if(!wanted)return;
    let live=true;
    readReference().then(reference=>{if(live)setPermissions(reference['x-cull-permissions']??[])}).catch(()=>{});
    return()=>{live=false};
  },[wanted]);
  return permissions;
}

export function Addons(){
  const list=useAddons();
  const [asking,setAsking]=useState<Addon|null>(null);
  const [busy,setBusy]=useState('');
  const [errors,setErrors]=useState<Record<string,string>>({});
  const ours=list?.filter(addon=>addon.builtIn)??[];
  const yours=list?.filter(addon=>!addon.builtIn)??[];
  const permissions=usePermissions(yours.some(addon=>(addon.permissions?.length??0)>0));
  async function turn(addon:Addon,on:boolean){
    setBusy(addon.id);setErrors(current=>({...current,[addon.id]:''}));
    try{await setAddon(addon.id,on)}
    catch(reason){setErrors(current=>({...current,[addon.id]:(reason as Error).message}))}
    finally{setBusy('')}
  }
  // An addon of your own is given a key the first time it is turned on, and
  // with it whatever it asked to be allowed, so that is said first.
  function toggle(addon:Addon){
    if(!addon.on&&!addon.builtIn&&(addon.permissions?.length??0)>0){setAsking(addon);return}
    void turn(addon,!addon.on);
  }
  const row=(addon:Addon)=><AddonRow key={addon.id} addon={addon} busy={busy===addon.id} disabled={busy!==''} error={errors[addon.id]} permissions={permissions} onToggle={()=>toggle(addon)}/>;
  return <section className="addons">
    <header className="addonshead">
      <h1>Addons</h1>
      <p className="ysum">Everything beyond reviewing dates, finding duplicates and the Bin is an addon. Turning one off takes its page away and stops what it does in the background. Nothing it did is undone, and files it moved to the Bin stay there.</p>
      <p className="addonslinks"><a href="/developers">API reference</a><a href="/developers#build">Build your own</a></p>
    </header>
    {list===null?<Busy size={64} label="Reading addons…"/>:<>
      <h2>Cull’s own</h2>
      <ul className="addonlist">{ours.map(row)}</ul>
      <h2>Yours</h2>
      {yours.length>0?<ul className="addonlist">{yours.map(row)}</ul>
        :<div className="addonempty">
          <span className="addontile"><Icon name="extension"/></span>
          <p>None yet. An addon of your own is a folder with an <code>addon.json</code> in Cull’s addons folder. It shows here a moment after it is put there, and does nothing until you turn it on. <a href="/developers#build">How to build one</a></p>
        </div>}
    </>}
    <PermissionDialog addon={asking} permissions={permissions} onCancel={()=>setAsking(null)} onConfirm={addon=>{setAsking(null);void turn(addon,true)}}/>
  </section>;
}

function AddonRow({addon,busy,disabled,error,permissions,onToggle}:{addon:Addon;busy:boolean;disabled:boolean;error?:string;permissions:Permission[]|null;onToggle:()=>void}){
  const title=`addon-${addon.id}`;
  const broken=!!addon.problem;
  const pages=addon.pages??[];
  return <li className={`addon${addon.on?' on':''}`}>
    <span className="addontile"><Icon name={addonIcon(addon.icon)} filled={addon.on}/></span>
    <div className="addonbody">
      <h3 id={title}>{addon.name}{!addon.builtIn&&<span className="addonver">{addon.version}</span>}</h3>
      <p className="addonsummary">{addon.summary}</p>
      <p className={`addonstatus ${addon.status.state}`}><span className="addondot" aria-hidden="true"/><b>{addon.on||addon.status.state==='problem'?stateLabels[addon.status.state]:'Off'}.</b> {addon.status.detail}</p>
      {error&&<p className="note warn" role="alert">{error}</p>}
      <details className="addonmore">
        <summary>About {addon.name}</summary>
        {addon.description&&<p>{addon.description}</p>}
        <dl className="kv">
          {pages.length>0&&<div><dt>Adds</dt><dd>{pages.map(page=><span key={page.id} className="addonpage"><Icon name={addonIcon(page.icon)}/>{addon.on?<a href={page.path}>{page.label}</a>:page.label}</span>)}</dd></div>}
          {(addon.needs?.length??0)>0&&<div><dt>Needs</dt><dd><ul>{addon.needs!.map(need=><li key={need}>{need}</li>)}</ul></dd></div>}
          {(addon.work?.length??0)>0&&<div><dt>While it is on</dt><dd><ul>{addon.work!.map(work=><li key={work}>{work}</li>)}</ul></dd></div>}
          {!addon.builtIn&&<div><dt>May</dt><dd><PermissionList names={addon.permissions??[]} permissions={permissions}/></dd></div>}
          {addon.routes>0&&<div><dt>API</dt><dd><a href={`/developers#${slug(addon.name)}`}>{plural(addon.routes,'route')} in the reference</a></dd></div>}
          {addon.folder&&<div><dt>Folder</dt><dd><code>{addon.folder}</code></dd></div>}
          {addon.author&&<div><dt>By</dt><dd>{addon.author}</dd></div>}
          {addon.homepage&&<div><dt>Homepage</dt><dd><a href={addon.homepage} target="_blank" rel="noreferrer noopener">{addon.homepage}</a></dd></div>}
        </dl>
      </details>
    </div>
    <button type="button" role="switch" className="switch" aria-checked={addon.on} aria-labelledby={title} aria-busy={busy||undefined} disabled={disabled||(broken&&!addon.on)}
      title={broken?'It cannot be turned on until the problem is fixed':undefined} onClick={onToggle}>
      <span className="knob"/>
    </button>
  </li>;
}

function PermissionList({names,permissions}:{names:string[];permissions:Permission[]|null}){
  const describe=(name:string)=>permissions?.find(permission=>permission.name===name)?.description??name;
  return <ul className="addonperms">
    <li><b>Read</b> {describe('read')}</li>
    {names.map(name=><li key={name} className={name==='delete'?'danger':undefined}><b>{name[0].toUpperCase()+name.slice(1)}</b> {describe(name)}</li>)}
  </ul>;
}

function PermissionDialog({addon,permissions,onCancel,onConfirm}:{addon:Addon|null;permissions:Permission[]|null;onCancel:()=>void;onConfirm:(addon:Addon)=>void}){
  const dialog=useRef<HTMLDialogElement>(null);
  useEffect(()=>{
    const element=dialog.current;
    if(!element)return;
    if(addon&&!element.open)element.showModal();
    if(!addon&&element.open)element.close();
  },[addon]);
  return <>
    {/* oxlint-disable-next-line jsx-a11y/click-events-have-key-events, jsx-a11y/no-noninteractive-element-interactions -- a click on the backdrop is the mouse's Esc */}
    <dialog ref={dialog} className="confirm" aria-labelledby="addon-ask-title" onClose={event=>{if(!event.currentTarget.open)onCancel()}} onClick={event=>{if(event.target===event.currentTarget)onCancel()}}>
      {addon&&<form method="dialog" onSubmit={event=>{event.preventDefault();onConfirm(addon)}}>
        <h2 id="addon-ask-title">Turn on {addon.name}?</h2>
        <p>Cull writes a key into its folder, and with it {addon.name} may:</p>
        <PermissionList names={addon.permissions??[]} permissions={permissions}/>
        <p>Turning it off again stops its key at once.</p>
        <div className="confirmacts">
          <button type="button" className="btn" autoFocus {...keyProps('Escape')} onClick={onCancel}>Cancel<Kbd keys="Escape"/></button>
          <button type="submit" className={`btn ${addon.permissions?.includes('delete')?'danger':'primary'}`} {...keyProps('Enter')}>Turn on<Kbd keys="Enter"/></button>
        </div>
      </form>}
    </dialog>
  </>;
}
