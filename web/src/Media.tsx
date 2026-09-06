import {useState} from 'react';
import type {Asset} from './api';
export function Media({asset,large=false,onReady}:{asset:Asset;large?:boolean;onReady?:(id:number)=>void}){
 const [failed,setFailed]=useState(false);
 const [play,setPlay]=useState(false);
 const preview=`/api/media/${asset.id}/preview?size=${large?'large':'grid'}`;
 if(failed)return <div className="media-missing"><span>Preview unavailable</span><small>{asset.kind.toUpperCase()} · Original untouched</small>{large&&<button onClick={()=>{setFailed(false);setPlay(false)}}>Retry preview</button>}</div>;
 if(large&&play)return <video className="actual-media" controls autoPlay playsInline poster={preview} src={`/api/media/${asset.id}/original`} onError={()=>setFailed(true)}/>;
 return <><img className="actual-media" src={preview} alt={asset.path.split('/').pop()||'Family memory'} loading={large?'eager':'lazy'} decoding={large?"sync":"async"} onLoad={e=>{const img=e.currentTarget;void img.decode().catch(()=>{}).then(()=>onReady?.(asset.id))}} onError={()=>setFailed(true)}/>{large&&asset.kind==='video'&&<button className="play-video" onClick={()=>setPlay(true)}>Play video</button>}{!large&&asset.kind==='video'&&<span className="video-badge">▶ Video</span>}</>;
}
