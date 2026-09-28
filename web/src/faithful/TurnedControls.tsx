import {useEffect,useState,type CSSProperties,type RefObject} from 'react';
import {Icon} from '../Icon';
import {tipProps} from './keys';

// A video's own controls are part of the video, so a clip turned in Cull
// would turn them too. While a clip is turned the viewer draws these instead,
// upright under it: play, where it is, how long it runs, and sound.

function clock(seconds:number){
  if(!Number.isFinite(seconds))return '0:00';
  const whole=Math.floor(seconds),hours=Math.floor(whole/3600),minutes=Math.floor(whole/60)%60,rest=String(whole%60).padStart(2,'0');
  return hours?`${hours}:${String(minutes).padStart(2,'0')}:${rest}`:`${minutes}:${rest}`;
}

export function TurnedControls({video}:{video:RefObject<HTMLVideoElement|null>}){
  const [state,setState]=useState({paused:true,time:0,duration:0,muted:true});
  useEffect(()=>{
    const node=video.current;
    if(!node)return;
    const read=()=>setState({paused:node.paused,time:node.currentTime,duration:node.duration||0,muted:node.muted});
    const events=['play','pause','timeupdate','durationchange','loadedmetadata','volumechange','seeked'];
    events.forEach(name=>node.addEventListener(name,read));
    // Clicking the clip plays or pauses it, as its own controls would.
    const toggle=()=>{if(node.paused)void node.play().catch(()=>{});else node.pause()};
    node.addEventListener('click',toggle);
    read();
    return()=>{events.forEach(name=>node.removeEventListener(name,read));node.removeEventListener('click',toggle)};
  },[video]);
  const node=video.current;
  const toggle=()=>{if(!node)return;if(node.paused)void node.play().catch(()=>{});else node.pause()};
  return <div className="rvplay" role="group" aria-label="Video">
    <button type="button" className="rvact" aria-label={state.paused?'Play':'Pause'} {...tipProps(state.paused?'Play':'Pause')} onClick={toggle}><Icon name={state.paused?'play_arrow':'pause'} filled/></button>
    <span className="rvtime">{clock(state.time)} / {clock(state.duration)}</span>
    <input type="range" aria-label="Position" min={0} max={state.duration||0} step="any" value={Math.min(state.time,state.duration||0)}
      style={{'--played':`${state.duration?state.time/state.duration*100:0}%`} as CSSProperties}
      onChange={event=>{if(node)node.currentTime=Number(event.currentTarget.value)}}/>
    <button type="button" className="rvact" aria-label={state.muted?'Turn the sound on':'Mute'} {...tipProps(state.muted?'Sound on':'Mute')} onClick={()=>{if(node)node.muted=!node.muted}}><Icon name={state.muted?'volume_off':'volume_up'}/></button>
  </div>;
}
