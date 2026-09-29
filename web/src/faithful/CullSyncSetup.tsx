import {useCallback,useEffect,useRef,useState} from 'react';
import {Busy} from '../Busy';
import {Kbd,keyProps,useDialogKeys} from './keys';

type SetupState='waiting'|'used'|'connected'|'expired';
type Setup={id:string;command?:string;state:SetupState;expires:string;now:string};
export type SetupAgent={online:boolean;access?:string;outdated?:boolean};

// copy puts the command on the clipboard. The page is usually opened over plain
// http on the tailnet or the LAN, where browsers do not offer the Clipboard API
// at all, so the older copy command on a selection is the fallback. Either way
// the command is left selected, so Command-C still works if both are refused.
export async function copy(text:string,from:HTMLElement|null):Promise<boolean>{
  const select=()=>{
    if(!from)return;
    const range=document.createRange();range.selectNodeContents(from);
    const selection=window.getSelection();selection?.removeAllRanges();selection?.addRange(range);
  };
  if(window.isSecureContext&&navigator.clipboard){
    try{await navigator.clipboard.writeText(text);select();return true}catch{/* fall back below */}
  }
  select();
  try{return document.execCommand('copy')}catch{return false}
}

/** Guides the reviewer through installing Cull Sync with one command. It asks
 * the server for a one-time command as it opens, follows that command until
 * the helper calls in with its new key and Photos access has been answered,
 * and then closes itself. */
export function CullSyncSetup({intro,agent,auto,onClose}:{intro:string;agent?:SetupAgent;auto:boolean;onClose:(connected:boolean)=>void}){
  const dialog=useRef<HTMLDialogElement>(null);
  useDialogKeys(dialog);
  const code=useRef<HTMLElement>(null);
  const [setup,setSetup]=useState<Setup|null>(null);
  const [command,setCommand]=useState('');
  const [error,setError]=useState('');
  const [copied,setCopied]=useState<''|'yes'|'no'>('');
  const done=useRef(false);

  useEffect(()=>{const element=dialog.current;if(element&&!element.open)element.showModal()},[]);
  const close=useCallback((connected:boolean)=>{if(done.current)return;done.current=true;dialog.current?.close();onClose(connected)},[onClose]);

  const fresh=useCallback(async()=>{
    setError('');setSetup(null);setCommand('');setCopied('');
    try{
      const response=await fetch('/api/photos/setup',{method:'POST',headers:{'Content-Type':'application/json'},body:'{}'});
      const result=await response.json() as Setup&{error?:string};
      if(!response.ok)throw new Error(result.error||'The server could not make a setup command.');
      setSetup(result);setCommand(result.command??'');
    }catch(reason){setError(reason instanceof SyntaxError?'The server did not answer. Check that Daddy Cull is running, then try again.':(reason as Error).message)}
  },[]);
  useEffect(()=>{void fresh()},[fresh]);

  // Follow the command until it has been used and the helper has called in.
  const id=setup?.id;
  const state=setup?.state;
  useEffect(()=>{
    if(!id||state==='expired'||state==='connected')return;
    const timer=window.setTimeout(async()=>{
      try{
        const response=await fetch(`/api/photos/setup/${id}`,{cache:'no-store'});
        // A restarted server has forgotten every command; this one is gone.
        if(response.status===404){setSetup(current=>current&&{...current,state:'expired'});return}
        if(!response.ok)throw new Error();
        const next=await response.json() as Setup;
        setSetup(current=>current&&current.id===next.id?{...current,...next,command:current.command}:current);
      }catch{setSetup(current=>current&&{...current})}
    },2000);
    return()=>window.clearTimeout(timer);
  },[id,state,setup]);

  // Done once Cull Sync is in and macOS has had its answer about Photos; if
  // access was refused, the page itself says how to put that right.
  const answered=!!agent&&agent.access!==undefined&&agent.access!=='notDetermined';
  useEffect(()=>{if(state==='connected'&&answered)close(true)},[state,answered,close]);
  // Opened by the page rather than asked for, it has nothing left to do once a
  // Mac that was only asleep calls in by itself.
  useEffect(()=>{if(auto&&agent?.online&&(!state||state==='waiting'||state==='expired'))close(false)},[auto,agent?.online,state,close]);

  async function onCopy(){setCopied(await copy(command,code.current)?'yes':'no')}

  let progress;
  if(error)progress=<p className="note warn" role="alert">{error}</p>;
  else if(!setup)progress=<Busy label="Preparing a command…" state="working"/>;
  else if(state==='expired')progress=<p className="note">This command has expired, so it will not work any more. Get a new one.</p>;
  else if(state==='connected')progress=<Busy label="Cull Sync is running. Click Allow on the Mac to let it use Photos." state="listening"/>;
  else if(state==='used')progress=<Busy label="Installing Cull Sync on the Mac. This takes a minute or so…" state="working"/>;
  else progress=<Busy label="Waiting for the command to be run on the Mac…" state="listening"/>;

  // No close on a click outside, unlike the Bin's question: selecting the
  // command by dragging can end outside the dialog, and that must not throw
  // the command away. Escape and Close still close it.
  return <dialog ref={dialog} className="confirm psetup" aria-labelledby="psetup-title" onClose={()=>close(false)}>
    <h2 id="psetup-title">{agent?.online&&agent.outdated?'Update Cull Sync on your Mac':'Set up Cull Sync on your Mac'}</h2>
    <p>{intro}</p>
    <ol className="psteps">
      <li>
        <b>Copy this command.</b> It works once, for the next 15 minutes.
        <div className="pcommand">
          <code ref={code}>{command||'…'}</code>
          <button type="button" className="btn small" disabled={!command||state==='expired'} {...keyProps('C')} onClick={()=>void onCopy()}>{copied==='yes'?'Copied':'Copy'}<Kbd keys="C"/></button>
        </div>
        {copied==='no'&&<span className="dim">The browser would not copy it. The command is selected: press Command-C.</span>}
      </li>
      <li><b>Paste it into Terminal on the Mac that has your Photos library</b>, then press Return. Terminal is in Applications, Utilities. If the Mac offers to install the command line developer tools, click Install, then come back here for a new command.</li>
      <li><b>Click Allow</b> when macOS asks whether Cull Sync may access your Photos. Choose full access if you are offered a choice.</li>
    </ol>
    <div className="psetupstate">{progress}</div>
    <div className="confirmacts">
      {(state==='expired'||error)&&<button type="button" className="btn primary" {...keyProps('Enter')} onClick={()=>void fresh()}>Get a new command<Kbd keys="Enter"/></button>}
      <button type="button" className="btn" {...keyProps('Escape')} onClick={()=>close(false)}>Close<Kbd keys="Escape"/></button>
    </div>
  </dialog>;
}
