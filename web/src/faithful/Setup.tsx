import {useCallback,useEffect,useRef,useState,type ReactNode} from 'react';
import {Busy} from '../Busy';
import {Icon} from '../Icon';
import {failure} from '../api';
import {CullSyncSetup,copy} from './CullSyncSetup';
import {setAddon} from './addonList';
import {navigate} from './router';
import {bytes} from './Bin';

export type SetupConfig={library:string;import:string;takeoutInbox:string;shared:boolean;icloud:{on:boolean;appleId:string;since:string};immich:{url:string;pathPrefix:string};jellyfin:{url:string};done:boolean};
type SetupRun={signedIn:boolean;lastRun:string;ok:boolean;message:string};
type SetupTool={name:string;found:boolean;for:string};
export type SetupView={configurable:boolean;config:SetupConfig;immichKeySet:boolean;jellyfinKeySet:boolean;tools:SetupTool[];icloud:SetupRun;applePhotos:SetupRun;free:number;version?:string;restarting?:boolean};

// The steps, in the order they are taken. Each after Folders is optional.
const steps=[
  {id:'welcome',label:'Welcome'},
  {id:'folders',label:'Folders'},
  {id:'icloud',label:'iCloud'},
  {id:'apple',label:'Apple Photos'},
  {id:'google',label:'Google Photos'},
  {id:'done',label:'Done'},
] as const;
type Step=typeof steps[number]['id'];

// Only needed for the step that uses them; the rest Daddy Cull needs to work.
const optional=new Set(['icloudpd','osxphotos']);
const install:Record<string,string>={exiftool:'brew install exiftool',ffmpeg:'brew install ffmpeg',icloudpd:'daddy-cull icloud install',osxphotos:'daddy-cull apple-photos install'};

export async function readSetup():Promise<SetupView>{
  const response=await fetch('/api/setup',{cache:'no-store'});
  if(!response.ok)throw new Error(await failure(response,'The setup could not be read.'));
  return response.json() as Promise<SetupView>;
}

const same=(a:SetupConfig,b:SetupConfig)=>JSON.stringify(a)===JSON.stringify(b);

/** Saves a change to the setup, then waits for Cull, which stops to take it up
 * and is started again by launchd, to answer with it. The answer is compared
 * with the setup as saved, since folders and addresses are tidied as they are
 * saved. Null means Cull did not answer again within 30 seconds. */
export async function saveSetup(change:{config:SetupConfig;immichKey?:string;jellyfinKey?:string},onRestarting?:()=>void):Promise<SetupView|null>{
  const response=await fetch('/api/setup',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(change)});
  if(!response.ok)throw new Error(await failure(response,'The setup could not be saved.'));
  const saved=(await response.json() as SetupView).config;
  onRestarting?.();
  const started=Date.now();
  await new Promise(resolve=>setTimeout(resolve,1200));
  for(;;){
    try{
      const answer=await readSetup();
      if(!answer.restarting&&same(answer.config,saved))return answer;
    }catch{/* still starting */}
    if(Date.now()-started>30000)return null;
    await new Promise(resolve=>setTimeout(resolve,800));
  }
}

function ago(value:string){
  const at=Date.parse(value);
  if(!Number.isFinite(at))return '';
  const minutes=Math.round((Date.now()-at)/60000);
  if(minutes<1)return 'just now';
  if(minutes<60)return `${minutes} minute${minutes===1?'':'s'} ago`;
  const hours=Math.round(minutes/60);
  if(hours<48)return `${hours} hour${hours===1?'':'s'} ago`;
  return `${Math.round(hours/24)} days ago`;
}

/** A Terminal command, with a button that copies it. */
function Command({text}:{text:string}){
  const code=useRef<HTMLElement>(null);
  const [copied,setCopied]=useState<''|'yes'|'no'>('');
  return <div className="setupcmd">
    <code ref={code}>{text}</code>
    <button type="button" className="btn small" onClick={()=>void copy(text,code.current).then(done=>setCopied(done?'yes':'no'))}>{copied==='yes'?'Copied':'Copy'}</button>
    {copied==='no'&&<span className="hint">The browser would not copy it. It is selected: press Command-C.</span>}
  </div>;
}

/** What a scheduled download last did. */
function LastRun({run,never}:{run:SetupRun;never:string}){
  if(!run.lastRun)return <p className="hint">{never}</p>;
  if(run.ok)return <p className="hint" role="status">Last run {ago(run.lastRun)}: {run.message||'finished.'}</p>;
  return <p className="note warn" role="status">Last try {ago(run.lastRun)}: {run.message||'it did not finish.'}</p>;
}

function Field({id,label,hint,children}:{id:string;label:string;hint?:ReactNode;children:ReactNode}){
  return <div className="setupfield">
    <label htmlFor={id}>{label}</label>
    {children}
    {hint&&<p className="hint">{hint}</p>}
  </div>;
}

/** A Takeout folder beside the Import folder, out of Downloads, which macOS
 * keeps from background apps. */
function takeoutSuggestion(importFolder:string){
  const parent=importFolder.replace(/\/+$/,'').split('/').slice(0,-1).join('/');
  return parent?`${parent}/Takeout`:'/Users/you/Pictures/Daddy Cull/Takeout';
}

/** The day folder a photo taken on 14 August 2019 goes into. */
function Tree({library}:{library:string}){
  const name=library.split('/').filter(Boolean).at(-1)||'Library';
  return <pre className="setuptree" aria-label={`A photo taken on 14 August 2019 is filed as ${name}/2019/2019-08/2019-08-14/IMG_0001.HEIC`}>{`${name}/
  2019/
    2019-08/
      2019-08-14/
        IMG_0001.HEIC
        IMG_0001.MOV`}</pre>;
}

/** Sets Daddy Cull up on this computer, one step at a time: the folders, then
 * each place photos can come from. Each change is saved as its step is left,
 * and Cull starts again to take it up. */
export function Setup({initial}:{initial:SetupView}){
  const [view,setView]=useState(initial);
  const [draft,setDraft]=useState<SetupConfig>(initial.config);
  const [step,setStep]=useState<Step>(()=>{
    const asked=new URLSearchParams(location.search).get('step');
    return steps.some(item=>item.id===asked)?asked as Step:'welcome';
  });
  const [saving,setSaving]=useState<''|'saving'|'restarting'|'gone'>('');
  const [error,setError]=useState('');
  const [sinceFrom,setSinceFrom]=useState(!!initial.config.icloud.since);
  const [cullSync,setCullSync]=useState(false);
  const heading=useRef<HTMLHeadingElement>(null);
  const editable=view.configurable;

  // The step is in the address, so a reload stays on it.
  useEffect(()=>{
    // The page is drawn once more as the app moves away, by then at the next
    // page's address, which the step is not for.
    if(location.pathname!=='/setup')return;
    const url=new URL(location.href);
    url.searchParams.set('step',step);
    history.replaceState(history.state,'',url.pathname+url.search);
    heading.current?.focus({preventScroll:true});
    window.scrollTo(0,0);
  },[step]);

  // While a download is waited on, its status is read again every few seconds.
  const waiting=(step==='icloud'&&draft.icloud.on&&!view.icloud.signedIn)||(step==='apple'&&!view.applePhotos.lastRun);
  useEffect(()=>{
    if(!waiting||saving)return;
    const timer=window.setInterval(()=>{readSetup().then(setView).catch(()=>{})},3000);
    return()=>window.clearInterval(timer);
  },[waiting,saving]);

  const changed=!same(draft,view.config);

  /** Saves the draft, then waits for Cull to come back with it. */
  const save=useCallback(async(config:SetupConfig):Promise<boolean>=>{
    setError('');setSaving('saving');
    try{
      const answer=await saveSetup({config},()=>setSaving('restarting'));
      if(!answer){setSaving('gone');return false}
      setView(answer);setDraft(answer.config);
      setSaving('');
      return true;
    }catch(reason){setError((reason as Error).message);setSaving('');return false}
  },[]);

  const index=steps.findIndex(item=>item.id===step);
  // Turning iCloud on stays on its step, where the sign-in comes next.
  const signInNext=step==='icloud'&&draft.icloud.on&&!view.icloud.signedIn&&changed;
  async function next(){
    if(editable&&changed&&!await save(draft))return;
    if(signInNext)return;
    if(step==='google'&&draft.takeoutInbox)await setAddon('google-photos',true).catch(()=>{});
    setStep(steps[Math.min(index+1,steps.length-1)].id);
  }
  async function finish(){
    if(editable&&!view.config.done&&!await save({...draft,done:true}))return;
    navigate('/');
  }
  const set=(change:Partial<SetupConfig>)=>setDraft(current=>({...current,...change}));
  const setICloud=(change:Partial<SetupConfig['icloud']>)=>setDraft(current=>({...current,icloud:{...current.icloud,...change}}));
  const busy=saving!=='';
  const foldersReady=!!draft.library.trim()&&!!draft.import.trim();
  const found=(name:string)=>view.tools.find(tool=>tool.name===name)?.found??false;

  let body:ReactNode;
  if(step==='welcome'){
    body=<>
      <p className="setuplead">Daddy Cull keeps your photos in one folder on this computer, a folder for every day, and gives you a few minutes a day to keep the good ones and remove the rest.</p>
      <ol className="setupflow" aria-label="How photos move through Daddy Cull">
        <li><Icon name="library_add"/><b>Import</b><span>Drop photos from any phone or camera here, or let iCloud, Apple Photos and Google Photos fill it.</span></li>
        <li><Icon name="calendar_month"/><b>Library</b><span>Each photo is filed under the day it was taken, never over another file.</span></li>
        <li><Icon name="photo"/><b>Review</b><span>Go through a day at a time: keep, remove or favourite. Removed photos wait in the Bin.</span></li>
      </ol>
      <h2>This computer</h2>
      <dl className="kv setuptools">
        {view.tools.map(tool=><div key={tool.name}><dt>{tool.name}</dt><dd>
          {tool.found?<span className="oktext"><Icon name="check"/> Installed</span>:optional.has(tool.name)?<span className="hint">Not installed, only needed if you use it</span>:<span className="warntext">Not installed</span>}
          <span className="hint">{tool.for}</span>
          {!tool.found&&install[tool.name]&&<Command text={install[tool.name]}/>}
        </dd></div>)}
        {view.free>0&&<div><dt>Free space</dt><dd>{bytes(view.free)}</dd></div>}
      </dl>
      {!editable&&<p className="note">This Daddy Cull was started with its folders set where it runs, such as in Docker, so they are shown here but changed there.</p>}
    </>;
  }else if(step==='folders'){
    body=<>
      <p className="setuplead">Two folders: the library that holds everything, and the Import folder new photos are dropped into.</p>
      <Field id="library" label="Library" hint="Each photo goes in the folder of the day it was taken. Already have folders laid out like this? Choose that folder, and Daddy Cull catalogues what is there.">
        <input id="library" className="setupinput" value={draft.library} disabled={!editable||busy} spellCheck={false} autoComplete="off" onChange={event=>set({library:event.target.value})}/>
        <Tree library={draft.library}/>
      </Field>
      <Field id="import" label="Import folder" hint={<>Photos in any other layout: copy them in here and each is filed by the day it was taken, read from the photo, then its name, then its file date. Nothing in it is deleted: a photo the library already has goes to <b>Already in the library</b> inside it, for you to check.</>}>
        <input id="import" className="setupinput" value={draft.import} disabled={!editable||busy} spellCheck={false} autoComplete="off" onChange={event=>set({import:event.target.value})}/>
      </Field>
      <label className="setupcheck">
        <input type="checkbox" checked={draft.shared} disabled={!editable||busy} onChange={event=>set({shared:event.target.checked})}/>
        <b>Let every user of this Mac change the library</b>
        <span className="hint">Leave this off unless someone else signs in to this Mac to cull too.</span>
      </label>
    </>;
  }else if(step==='icloud'){
    body=<>
      <p className="setuplead">Download what is in iCloud Photos to this Mac every six hours, with <a href="https://github.com/icloud-photos-downloader/icloud_photos_downloader" target="_blank" rel="noreferrer">icloudpd</a>, and file each photo under the day it was taken. Photos stay in iCloud; this is a copy you own.</p>
      <label className="setupcheck">
        <input type="checkbox" checked={draft.icloud.on} disabled={!editable||busy} onChange={event=>setICloud({on:event.target.checked})}/>
        <b>Download my iCloud Photos</b>
      </label>
      {draft.icloud.on&&<>
        <Field id="appleid" label="Apple Account" hint="The email address or phone number you sign in to iCloud with.">
          <input id="appleid" className="setupinput" inputMode="email" value={draft.icloud.appleId} disabled={!editable||busy} spellCheck={false} autoComplete="off" onChange={event=>setICloud({appleId:event.target.value})}/>
        </Field>
        <div className="setupfield">
          <span className="setuplabel">Download</span>
          <div className="segmented" role="radiogroup" aria-label="What to download">
            <label className={sinceFrom?undefined:'on'}><input type="radio" name="since" checked={!sinceFrom} disabled={!editable||busy} onChange={()=>{setSinceFrom(false);setICloud({since:''})}}/>Everything</label>
            <label className={sinceFrom?'on':undefined}><input type="radio" name="since" checked={sinceFrom} disabled={!editable||busy} onChange={()=>{setSinceFrom(true);setICloud({since:draft.icloud.since||new Date().toISOString().slice(0,10)})}}/>From a day on</label>
          </div>
          {sinceFrom&&<input className="setupinput short" type="date" aria-label="First day to download" value={draft.icloud.since} disabled={!editable||busy} onChange={event=>setICloud({since:event.target.value})}/>}
          <p className="hint">A whole library can take days the first time. Choose a day to start with only what is new.</p>
        </div>
        {view.config.icloud.on&&!changed?<div className="setupsteps">
          <h2>Sign in</h2>
          <ol>
            {!found('icloudpd')&&<li><b>Install icloudpd.</b> Paste this into Terminal, in Applications, Utilities:<Command text="daddy-cull icloud install"/></li>}
            <li><b>Sign in from Terminal.</b> Your password and the six-digit code go to Apple from there, never through this page.<Command text="daddy-cull icloud sign-in"/></li>
          </ol>
          {view.icloud.signedIn
            ?<p className="note ok setupok" role="status"><Icon name="check"/><span>Signed in. Apple asks you to sign in again about every two months; this page and the Import status in Settings will say when.</span></p>
            :<Busy label="Waiting for you to sign in in Terminal…" state="listening"/>}
          <LastRun run={view.icloud} never="Nothing downloaded yet."/>
        </div>:draft.icloud.on&&<p className="hint">Save, then sign in from Terminal.</p>}
      </>}
    </>;
  }else if(step==='apple'){
    body=<>
      <p className="setuplead">Photos on this Mac can come across once or whenever you like, and what you remove in Daddy Cull can leave Photos too. Both are optional.</p>
      <div className="setupsteps">
        <h2>Bring in your Photos library</h2>
        <p className="setuptext">Copies each photo, the edited version where you edited one, into <b>Import/Apple Photos</b> with <a href="https://github.com/RhetTbull/osxphotos" target="_blank" rel="noreferrer">osxphotos</a>. Run it again any time: only photos not brought across before come. macOS asks whether Terminal may use Photos: click Allow.</p>
        <Command text="daddy-cull apple-photos import"/>
        <LastRun run={view.applePhotos} never="Not run yet."/>
      </div>
      <div className="setupsteps">
        <h2>Keep Photos in step</h2>
        <p className="setuptext">Cull Sync, a small app on this Mac, finds each photo you removed in Daddy Cull in Photos, shows you the list, and changes nothing until you apply. Deleted photos go to Recently Deleted.</p>
        <button type="button" className="btn" onClick={()=>setCullSync(true)}>Set up Cull Sync</button>
      </div>
      {cullSync&&<CullSyncSetup auto={false} intro="Cull Sync carries what you remove here across to Photos on this Mac." onClose={()=>setCullSync(false)}/>}
    </>;
  }else if(step==='google'){
    body=<>
      <p className="setuplead">Google Photos has no way for other apps to download a whole library any more, so photos come across in a Google Takeout export.</p>
      <Field id="takeout" label="Takeout folder" hint="Leave this empty if you do not use Google Photos.">
        <input id="takeout" className="setupinput" value={draft.takeoutInbox} disabled={!editable||busy} spellCheck={false} autoComplete="off" placeholder={takeoutSuggestion(draft.import)} onChange={event=>set({takeoutInbox:event.target.value})}/>
      </Field>
      <ol className="setuplist">
        <li>At <a href="https://takeout.google.com" target="_blank" rel="noreferrer">takeout.google.com</a>, deselect all, select Google Photos, and export as .zip or .tgz files.</li>
        <li>Download the files into this folder, without unpacking them.</li>
        <li>Open <b>Google Photos</b> in the sidebar: it shows which photos the library lacks, and adds them under the day each was taken.</li>
      </ol>
    </>;
  }else{
    const c=view.config;
    body=<>
      <p className="setuplead">That is everything. Anything here can be changed later: Settings has a link back to this page.</p>
      <dl className="kv">
        <div><dt>Library</dt><dd>{c.library||'Not chosen'}</dd></div>
        <div><dt>Import folder</dt><dd>{c.import||'Not chosen'}</dd></div>
        <div><dt>iCloud Photos</dt><dd>{c.icloud.on?`${c.icloud.appleId}${c.icloud.since?`, from ${c.icloud.since}`:''}${view.icloud.signedIn?'':' · not signed in yet'}`:'Off'}</dd></div>
        <div><dt>Apple Photos</dt><dd>{view.applePhotos.lastRun?`Brought in ${ago(view.applePhotos.lastRun)}`:'Not brought in'}</dd></div>
        <div><dt>Google Takeout</dt><dd>{c.takeoutInbox||'Off'}</dd></div>
      </dl>
      <p className="hint">New photos appear on Today, the day they were taken, once they are filed. Filing happens within a minute of a photo arriving.</p>
    </>;
  }

  return <section className="setup">
    <h1 ref={heading} tabIndex={-1}>Set up Daddy Cull</h1>
    <ol className="setuprail" aria-label="Steps">
      {steps.map((item,at)=><li key={item.id} className={`${item.id===step?'on':''}${at<index?' past':''}`||undefined} aria-current={item.id===step?'step':undefined}>
        <button type="button" aria-label={item.label} disabled={busy||(!foldersReady&&at>1)} onClick={()=>setStep(item.id)}><span className="setupnum">{at<index?<Icon name="check"/>:at+1}</span><span className="setupname">{item.label}</span></button>
      </li>)}
    </ol>
    <div className="setupbody">
      <h2 className="setupstep">{steps[index].label}</h2>
      {body}
      {error&&<p className="note warn" role="alert">{error}</p>}
      {saving==='saving'&&<p className="hint"><Busy label="Saving…" state="working"/></p>}
      {saving==='restarting'&&<p className="hint"><Busy label="Daddy Cull is starting again with the change…" state="working"/></p>}
      {saving==='gone'&&<p className="note warn" role="alert">Daddy Cull stopped to take the change up and has not come back. Run <code>daddy-cull start</code> in Terminal, then reload this page.</p>}
    </div>
    <div className="setupacts">
      {index>0&&<button type="button" className="btn" disabled={busy} onClick={()=>setStep(steps[index-1].id)}>Back</button>}
      <span className="setupgap"/>
      {index>1&&step!=='done'&&editable&&changed&&<button type="button" className="btn" disabled={busy} onClick={()=>{setDraft(view.config);setStep(steps[index+1].id)}}>Skip</button>}
      {step==='done'
        ?<button type="button" className="btn primary" disabled={busy} onClick={()=>void finish()}>Start culling</button>
        :<button type="button" className="btn primary" disabled={busy||(step==='folders'&&!foldersReady)} onClick={()=>void next()}>{signInNext?'Save and sign in':editable&&changed?'Save and continue':step==='welcome'?'Begin':'Continue'}</button>}
    </div>
  </section>;
}
