import {useEffect,useLayoutEffect,useRef,useState,type RefObject} from 'react';
import {createPortal} from 'react-dom';

// Every action that has a key says so in the same way: a button carries its
// key in aria-keyshortcuts and its name in data-tip, and one tooltip draws
// both, name then key, wherever the pointer rests or the keyboard lands. A
// shortcut is written the way aria-keyshortcuts is ("Shift+R", "Delete"),
// with Mod for Command on a Mac and Control elsewhere.

export const MAC=/Mac|iPhone|iPad/.test(navigator.platform);

const faces:Record<string,string>=MAC
  ?{Mod:'⌘',Meta:'⌘',Control:'⌃',Shift:'⇧',Alt:'⌥',Enter:'↵',Escape:'Esc',Delete:'⌫',ArrowLeft:'←',ArrowRight:'→',ArrowUp:'↑',ArrowDown:'↓',Space:'Space'}
  :{Mod:'Ctrl',Meta:'Win',Control:'Ctrl',Shift:'Shift',Alt:'Alt',Enter:'Enter',Escape:'Esc',Delete:'Del',ArrowLeft:'←',ArrowRight:'→',ArrowUp:'↑',ArrowDown:'↓',Space:'Space'};

/** A shortcut as aria-keyshortcuts wants it, Mod spelt out for this machine.
 * Several shortcuts for one action are separated by spaces. */
export function ariaKeys(spec:string){
  return spec.split(' ').map(one=>one.split('+').map(part=>part==='Mod'?(MAC?'Meta':'Control'):part).join('+')).join(' ');
}

/** The keys drawn for one shortcut, one chip per key. */
function chips(spec:string){
  return spec.split('+').map(part=>faces[part]??part.toUpperCase());
}

/** A shortcut drawn as keys. Only the first of several is shown. */
export function Kbd({keys}:{keys:string}){
  return <span className="kbds" aria-hidden="true">{chips(keys.split(' ')[0]).map((key,index)=><kbd key={index}>{key}</kbd>)}</span>;
}

/** Whether a key went to a field being typed in, which single-key shortcuts
 * leave alone. */
export function typing(target:EventTarget|null){
  return target instanceof HTMLInputElement||target instanceof HTMLTextAreaElement||target instanceof HTMLSelectElement||(target instanceof HTMLElement&&target.isContentEditable);
}

/** Whether a key press is one of the shortcuts in `spec`. Delete is either
 * delete key, since a Mac's is Backspace. */
export function matches(event:KeyboardEvent,spec:string){
  return spec.split(' ').some(one=>{
    const parts=one.split('+'),key=parts.at(-1)!;
    const mod=parts.includes('Mod');
    if((MAC?event.metaKey:event.ctrlKey)!==mod)return false;
    if(event.altKey!==parts.includes('Alt'))return false;
    if(!MAC&&!mod&&event.metaKey)return false;
    if(MAC&&!mod&&event.ctrlKey)return false;
    // A key such as ? or / is itself typed with Shift on most layouts, so
    // Shift only counts for letters and named keys.
    const letter=key.length===1&&/[a-z]/i.test(key);
    if((letter||key.length>1)&&event.shiftKey!==parts.includes('Shift'))return false;
    if(key==='Delete')return event.key==='Delete'||event.key==='Backspace';
    if(key==='Space')return event.key===' ';
    return key.length===1?event.key.toLowerCase()===key.toLowerCase():event.key===key;
  });
}

/** Whether something is open over the page, a photo or a question, which
 * the page's own shortcuts then leave alone. */
export function covered(){
  return !!document.querySelector('[aria-modal="true"], dialog[open]');
}

/** Runs `run` when `spec` is pressed on the page, not in a field, with
 * nothing open over it. The latest `run` is always the one called. */
export function useShortcut(spec:string|undefined,run:()=>void,enabled=true){
  const latest=useRef(run);
  useLayoutEffect(()=>{latest.current=run});
  useEffect(()=>{
    if(!spec||!enabled)return;
    const key=(event:KeyboardEvent)=>{
      if(event.defaultPrevented||event.repeat||typing(event.target)||covered()||!matches(event,spec))return;
      event.preventDefault();
      latest.current();
    };
    window.addEventListener('keydown',key);
    return()=>window.removeEventListener('keydown',key);
  },[spec,enabled]);
}

/** A question's buttons answer to the keys drawn on them: Return presses the
 * one marked Enter wherever the focus is, as a Mac's own questions do, and
 * Esc is the dialog's own way out. */
export function useDialogKeys(dialog:RefObject<HTMLDialogElement|null>){
  useEffect(()=>{
    // Listened for on the window, since the dialog may be drawn after this
    // runs, and only while it is open with the key pressed inside it.
    const key=(event:KeyboardEvent)=>{
      const node=dialog.current;
      if(!node?.open||!(event.target instanceof Node&&node.contains(event.target)))return;
      if(event.repeat||event.key==='Escape'||(typing(event.target)&&event.key!=='Enter'))return;
      const button=[...node.querySelectorAll<HTMLButtonElement|HTMLAnchorElement>('[data-keys]')].find(one=>one.dataset.keys!=='Escape'&&matches(event,one.dataset.keys!));
      if(!button)return;
      event.preventDefault();
      if(!(button instanceof HTMLButtonElement&&button.disabled))button.click();
    };
    window.addEventListener('keydown',key);
    return()=>window.removeEventListener('keydown',key);
  },[dialog]);
}

type Shown={text:string;keys:string;box:DOMRect};

/** The one tooltip, mounted once by the frame. */
export function Tips(){
  const [shown,setShown]=useState<Shown|null>(null);
  const tip=useRef<HTMLDivElement>(null);
  const [place,setPlace]=useState<{left:number;top:number}|null>(null);
  useEffect(()=>{
    let timer=0,recent=0,over:Element|null=null,shownRecently=false;
    const read=(element:Element):Shown|null=>{
      const text=element.getAttribute('data-tip')??'';
      const keys=element.getAttribute('data-keys')??'';
      return text||keys?{text,keys,box:element.getBoundingClientRect()}:null;
    };
    const hide=()=>{clearTimeout(timer);over=null;setShown(null)};
    const enter=(event:PointerEvent)=>{
      if(event.pointerType==='touch')return;
      const element=event.target instanceof Element?event.target.closest('[data-tip]'):null;
      if(element===over)return;
      clearTimeout(timer);setShown(null);over=element;
      if(!element)return;
      // As Google Photos does, a moment's rest before the first tip; moving
      // along a row of buttons then shows each at once.
      timer=window.setTimeout(()=>{if(over===element&&element.isConnected)setShown(read(element))},shownRecently?80:450);
    };
    const focus=(event:FocusEvent)=>{
      const element=event.target instanceof Element?event.target.closest('[data-tip]'):null;
      if(!element||!element.matches(':focus-visible'))return;
      clearTimeout(timer);over=element;setShown(read(element));
    };
    const remember=()=>{shownRecently=true;clearTimeout(recent);recent=window.setTimeout(()=>{shownRecently=false},900)};
    const leave=(event:PointerEvent)=>{if(over&&!(event.relatedTarget instanceof Node&&over.contains(event.relatedTarget))){remember();hide()}};
    document.addEventListener('pointerover',enter);
    document.addEventListener('pointerout',leave);
    document.addEventListener('focusin',focus);
    document.addEventListener('focusout',hide);
    document.addEventListener('pointerdown',hide,true);
    document.addEventListener('keydown',hide,true);
    window.addEventListener('scroll',hide,true);
    window.addEventListener('blur',hide);
    return()=>{
      clearTimeout(timer);clearTimeout(recent);
      document.removeEventListener('pointerover',enter);
      document.removeEventListener('pointerout',leave);
      document.removeEventListener('focusin',focus);
      document.removeEventListener('focusout',hide);
      document.removeEventListener('pointerdown',hide,true);
      document.removeEventListener('keydown',hide,true);
      window.removeEventListener('scroll',hide,true);
      window.removeEventListener('blur',hide);
    };
  },[]);
  // Below the control, centred on it, kept on screen; above it when there is
  // no room below.
  useLayoutEffect(()=>{
    const node=tip.current;
    if(!shown||!node){setPlace(null);return}
    const width=node.offsetWidth,height=node.offsetHeight,gap=8,edge=8;
    const left=Math.min(innerWidth-edge-width,Math.max(edge,shown.box.left+shown.box.width/2-width/2));
    const below=shown.box.bottom+gap;
    const top=below+height+edge>innerHeight?shown.box.top-gap-height:below;
    setPlace({left,top});
  },[shown]);
  if(!shown)return null;
  return createPortal(<div ref={tip} className="tip" role="tooltip" style={place?{left:place.left,top:place.top}:{visibility:'hidden'}}>
    {shown.text&&<span>{shown.text}</span>}{shown.keys&&<Kbd keys={shown.keys}/>}
  </div>,document.body);
}

/** The attributes a control with a key carries, for one whose label and
 * key are already in sight. */
export function keyProps(keys:string){
  return {'data-keys':keys,'aria-keyshortcuts':ariaKeys(keys)};
}

/** The attributes a control with a tooltip, and maybe a key, carries. */
export function tipProps(text:string,keys?:string){
  return {'data-tip':text,...(keys?keyProps(keys):{})};
}
