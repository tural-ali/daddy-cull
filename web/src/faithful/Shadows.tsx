import {Media} from '../Media';
import type {Asset} from '../api';

type ShadowMember=Asset&{disk:string;relativePath:string;fullHash?:string};
type ShadowGroup={kind:string;key:string;verified:boolean;size:number;reclaimable:number;members:ShadowMember[]};
function bytes(value:number){return value<1024**2?`${(value/1024).toFixed(1)} KB`:value<1024**3?`${(value/1024**2).toFixed(1)} MB`:`${(value/1024**3).toFixed(1)} GB`}

export function Shadows({groups}:{groups:ShadowGroup[]}){
  const exact=groups.filter(group=>group.verified).length;
  return <>
    <section className="dupehead"><h1>Shadowed</h1><p className="ysum"><b>{groups.length.toLocaleString()}</b> colliding paths · <b>{exact.toLocaleString()}</b> fully hash-verified</p><p className="note">These are physical disk copies hidden by the merged user share, plus names that differ only by letter case. The scan and the cached hash evidence were imported from the earlier PHP tool's database; this app is Go throughout and has not re-walked the disks itself, so treat the collision list as of the import date. Cleanup stays disabled until the disk-specific Go writer has passed no-overwrite and recovery tests.</p></section>
    {groups.map((group,index)=><div className="xgroup" key={`${group.kind}:${group.key}`}><p className="xmeta"><span className="gnum">{index+1}</span><span className={`b ${group.kind==='shadowed'?'shadowed':'casey'}`}>{group.kind==='shadowed'?'hidden by the share':'same name, different case'}</span>{group.members.length} copies · {bytes(group.size)} each {group.verified?<><strong>· byte-identical</strong> · {bytes(group.reclaimable)} reclaimable</>:<span className="dim">· not fully verified</span>}</p><div className="gal tight">{group.members.map(member=><figure className="mo" key={member.id}><Media asset={member}/><div className="bdg"><span className="b">{member.disk}</span>{member.fullHash&&<span className="b keep">hashed</span>}</div><figcaption className="cap"><span>{member.relativePath.split('/').pop()}</span><span className="dim">{member.disk}</span></figcaption></figure>)}</div></div>)}
  </>;
}
