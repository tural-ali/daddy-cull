import {useState} from 'react';
import type {Asset} from './api';

// FilePreview draws a still for one file served by a media route, and falls
// back to an honest "preview unavailable" when that route has nothing to give.
// It is separate from Media because the Bin holds files the catalogue does not:
// a culled photograph still has to be looked at before it is restored or
// destroyed, and deciding that from a filename alone is not a real choice.
export function FilePreview({base,name,kind,large=false,onReady}:{base:string;name:string;kind:string;large?:boolean;onReady?:()=>void}){
 const [failed,setFailed]=useState(false);
 const [play,setPlay]=useState(false);
 const preview=`${base}/preview?size=${large?'large':'grid'}`;
 if(failed)return <div className="media-missing"><span>Preview unavailable</span><small>{kind.toUpperCase()} · Original untouched</small>{large&&<button onClick={()=>{setFailed(false);setPlay(false)}}>Retry preview</button>}</div>;
 if(large&&play)return <video className="actual-media" controls autoPlay playsInline poster={preview} src={`${base}/original`} onError={()=>setFailed(true)}/>;
 return <><img className="actual-media" src={preview} alt={name||'Family memory'} loading={large?'eager':'lazy'} decoding={large?"sync":"async"} onLoad={e=>{const img=e.currentTarget;void img.decode().catch(()=>{}).then(()=>onReady?.())}} onError={()=>setFailed(true)}/>{large&&kind==='video'&&<button className="play-video" onClick={()=>setPlay(true)}>Play video</button>}{!large&&kind==='video'&&<span className="video-badge">▶ Video</span>}</>;
}

export function Media({asset,large=false,onReady}:{asset:Asset;large?:boolean;onReady?:(id:number)=>void}){
 return <FilePreview base={`/api/media/${asset.id}`} name={asset.path.split('/').pop()||''} kind={asset.kind} large={large} onReady={()=>onReady?.(asset.id)}/>;
}
