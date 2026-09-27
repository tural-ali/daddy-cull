import {useEffect,useImperativeHandle,useRef,type Ref,type VideoHTMLAttributes} from 'react';

const storageKey='cull.video-sound';
type Sound={muted:boolean;volume:number};

// The reviewer's preference from Settings, read with the stats. Until it
// arrives clips start muted, the safer of the two.
let preferMuted=true;
export function setVideoSoundPreference(muted:boolean){preferMuted=muted}

function saved():Sound{
  try{
    const value=JSON.parse(sessionStorage.getItem(storageKey)??'null');
    if(value&&typeof value.muted==='boolean'&&typeof value.volume==='number')return {muted:value.muted,volume:Math.min(1,Math.max(0,value.volume))};
  }catch{/* storage blocked: fall back to the preference */}
  return {muted:preferMuted,volume:1};
}

function save(sound:Sound){try{sessionStorage.setItem(storageKey,JSON.stringify(sound))}catch{/* storage blocked: this clip still plays as set */}}

/** Forgets this tab's sound choice, so the next clip follows the preference. */
export function clearSessionSound(){try{sessionStorage.removeItem(storageKey)}catch{/* nothing was saved */}}

// SessionVideo starts every clip the way Settings says, muted unless the
// reviewer chose sound. Once the viewer turns the sound on or off or changes
// the volume, every clip opened later in this tab plays the same way, until
// the tab is closed. A browser that refuses to autoplay with sound (a reload
// clears the permission a click earned) gets a muted start instead, and that
// forced mute is not remembered as the viewer's choice.
export function SessionVideo({ref,autoPlay,onVolumeChange,...props}:VideoHTMLAttributes<HTMLVideoElement>&{ref?:Ref<HTMLVideoElement>}){
  const node=useRef<HTMLVideoElement>(null);
  const forcedMute=useRef(false);
  useImperativeHandle(ref,()=>node.current as HTMLVideoElement);
  useEffect(()=>{
    const video=node.current;
    if(!video)return;
    const sound=saved();
    forcedMute.current=false;
    video.volume=sound.volume;
    video.muted=sound.muted;
    if(!autoPlay)return;
    video.play().catch((error:unknown)=>{
      if((error as DOMException|null)?.name!=='NotAllowedError'||video.muted)return;
      forcedMute.current=true;
      video.muted=true;
      void video.play().catch(()=>{});
    });
  },[props.src,autoPlay]);
  return <video ref={node} muted {...props} onVolumeChange={event=>{
    const video=event.currentTarget;
    if(forcedMute.current&&video.muted)return;
    forcedMute.current=false;
    save({muted:video.muted,volume:video.volume});
    onVolumeChange?.(event);
  }}/>;
}
