import {Fragment,useDeferredValue,useEffect,useMemo,useState,type ReactNode} from 'react';
import {Busy} from '../Busy';
import {Icon} from '../Icon';
import {useAddons} from './addonList';
import {readReference,type Operation,type Parameter,type Reference,type Schema} from './reference';

// The API Cull's own pages use, open to addons, read from the description
// the server gives of itself. Every route says what it takes, what it gives
// back and when it fails; a route that only reads can be tried from here.

type Route={method:string;path:string;op:Operation};

const methodOrder=['get','post','put','patch','delete'];
function slug(text:string){return text.toLowerCase().replace(/[^a-z0-9]+/g,'-').replace(/^-|-$/g,'')}

/** Backticks in the server's words are code. */
function prose(text:string|undefined):ReactNode{
  if(!text)return null;
  return text.split(/(`[^`]+`)/).map((part,index)=>part.startsWith('`')&&part.endsWith('`')&&part.length>2?<code key={index}>{part.slice(1,-1)}</code>:<Fragment key={index}>{part}</Fragment>);
}

function refName(ref:string){return ref.split('/').at(-1)??ref}

function resolve(reference:Reference,schema:Schema):{name?:string;schema:Schema}{
  if(schema.$ref){const name=refName(schema.$ref);return {name,schema:reference.components.schemas[name]??{}}}
  return {schema};
}

/** A schema's type in a few words: a name, a list of one, or a choice. */
function typeName(schema:Schema|undefined):string{
  if(!schema)return 'any';
  if(schema.$ref)return refName(schema.$ref);
  const choices=schema.anyOf??schema.oneOf;
  if(choices){
    const named=choices.filter(choice=>choice.type!=='null').map(choice=>typeName(choice));
    return choices.some(choice=>choice.type==='null')?`${named.join(' or ')} or null`:named.join(' or ');
  }
  if(schema.enum)return schema.enum.map(value=>JSON.stringify(value)).join(' | ');
  const type=Array.isArray(schema.type)?schema.type.filter(kind=>kind!=='null').join(' or ')+(schema.type.includes('null')?' or null':''):schema.type;
  if(type==='array')return `${typeName(schema.items)}[]`;
  if(type==='object'&&schema.additionalProperties&&typeof schema.additionalProperties==='object')return `map of ${typeName(schema.additionalProperties)}`;
  return (type??'any')+(schema.format?` (${schema.format})`:'');
}

/** The object a schema describes, if it is one, through lists and names. */
function objectOf(reference:Reference,schema:Schema|undefined,seen:string[]):{name?:string;schema:Schema}|null{
  if(!schema)return null;
  if(schema.$ref){
    const name=refName(schema.$ref);
    if(seen.includes(name))return null;
    const target=reference.components.schemas[name];
    return target?.properties?{name,schema:target}:objectOf(reference,target,[...seen,name]);
  }
  if(schema.items)return objectOf(reference,schema.items,seen);
  const choice=(schema.anyOf??schema.oneOf)?.find(option=>option.type!=='null');
  if(choice)return objectOf(reference,choice,seen);
  if(schema.additionalProperties&&typeof schema.additionalProperties==='object')return objectOf(reference,schema.additionalProperties,seen);
  return schema.properties?{schema}:null;
}

/** An object's fields, each with its type and what it holds. A field that is
 * an object itself opens to its own fields. */
function Fields({reference,schema,seen}:{reference:Reference;schema:Schema;seen:string[]}){
  const properties=schema.properties??{};
  const order=schema['x-order']??Object.keys(properties);
  return <ul className="fields">
    {order.filter(name=>properties[name]).map(name=>{
      const field=properties[name],inner=objectOf(reference,field,seen);
      const head=<><code className="fname">{name}</code><span className="ftype">{typeName(field)}</span>{schema.required?.includes(name)&&<span className="freq">required</span>}</>;
      return <li key={name}>
        {inner?<details><summary>{head}</summary><p className="fdoc">{prose(field.description)}</p>
          <Fields reference={reference} schema={inner.schema} seen={inner.name?[...seen,inner.name]:seen}/></details>
          :<><div className="fhead">{head}</div><p className="fdoc">{prose(field.description)}</p></>}
      </li>;
    })}
  </ul>;
}

function SchemaView({reference,schema}:{reference:Reference;schema:Schema}){
  const {name,schema:target}=resolve(reference,schema);
  const inner=objectOf(reference,schema,[]);
  return <div className="schema">
    <p className="stype"><span className="ftype">{typeName(schema)}</span>{prose(name?target.description:schema.description)}</p>
    {inner&&<Fields reference={reference} schema={inner.schema} seen={inner.name?[inner.name]:[]}/>}
  </div>;
}

/** A value of the right shape, for the examples. */
function example(reference:Reference,schema:Schema|undefined,depth=0):unknown{
  if(!schema||depth>4)return null;
  if(schema.example!==undefined)return schema.example;
  if(schema.$ref)return example(reference,reference.components.schemas[refName(schema.$ref)],depth+1);
  const choice=(schema.anyOf??schema.oneOf)?.find(option=>option.type!=='null');
  if(choice)return example(reference,choice,depth);
  if(schema.enum)return schema.enum[0];
  const type=Array.isArray(schema.type)?schema.type.find(kind=>kind!=='null'):schema.type;
  if(type==='object'||schema.properties){
    const order=schema['x-order']??Object.keys(schema.properties??{});
    return Object.fromEntries(order.filter(name=>schema.properties?.[name]).map(name=>[name,example(reference,schema.properties![name],depth+1)]));
  }
  if(type==='array')return [example(reference,schema.items,depth+1)];
  if(type==='integer'||type==='number')return 0;
  if(type==='boolean')return true;
  return '';
}

function paramValue(param:Parameter){
  const value=param.example;
  return value===undefined||value===null?'':typeof value==='string'?value:JSON.stringify(value);
}

function address(route:Route,values:Record<string,string>){
  // A path parameter not filled in yet shows as <name>, which neither the
  // shell nor curl reads as anything but text; curl would expand {name}.
  const path=route.path.replace(/\{([^}]+)\}/g,(_,name:string)=>values[name]?encodeURIComponent(values[name]):`<${name}>`);
  const query=new URLSearchParams();
  for(const param of route.op.parameters??[])if(param.in==='query'&&values[param.name])query.set(param.name,values[param.name]);
  const search=query.toString();
  return search?`${path}?${search}`:path;
}

function curl(reference:Reference,route:Route,values:Record<string,string>){
  const lines=[`curl${route.method==='get'?'':` -X ${route.method.toUpperCase()}`} \\`,'  -H "Authorization: Bearer $(cat key)" \\'];
  const body=route.op.requestBody?.content['application/json']?.schema;
  if(body)lines.push('  -H "Content-Type: application/json" \\',`  -d '${JSON.stringify(example(reference,body)).replaceAll("'","'\\''")}' \\`);
  lines.push(`  "${location.origin}${address(route,values)}"`);
  return lines.join('\n');
}

function streams(route:Route){return Object.values(route.op.responses).some(response=>Object.keys(response.content??{}).some(type=>!type.includes('json')))}

function Copy({text,label}:{text:string;label:string}){
  const [done,setDone]=useState(false);
  useEffect(()=>{if(!done)return;const timer=setTimeout(()=>setDone(false),1600);return()=>clearTimeout(timer)},[done]);
  return <button type="button" className="textbtn copybtn" onClick={()=>void navigator.clipboard?.writeText(text).then(()=>setDone(true))}>
    <Icon name={done?'check':'content_copy'}/>{done?'Copied':label}
  </button>;
}

function Code({text,label='Copy'}:{text:string;label?:string}){
  return <div className="codeblock"><pre><code>{text}</code></pre><Copy text={text} label={label}/></div>;
}

/** Sends a route that only reads, from this page, and shows what came back. */
function TryIt({route,values}:{route:Route;values:Record<string,string>}){
  const [answer,setAnswer]=useState<{status:number;type:string;body:string}|null>(null);
  const [busy,setBusy]=useState(false);
  const missing=(route.op.parameters??[]).filter(param=>param.in==='path'&&!values[param.name]).map(param=>param.name);
  async function send(){
    setBusy(true);
    try{
      const response=await fetch(address(route,values),{cache:'no-store'});
      const type=response.headers.get('Content-Type')??'';
      let body:string;
      if(type.includes('json')){const text=await response.text();try{body=JSON.stringify(JSON.parse(text),null,2)}catch{body=text}}
      else body=`${type||'A file'}, ${(await response.blob()).size.toLocaleString()} bytes`;
      setAnswer({status:response.status,type,body:body.length>40000?`${body.slice(0,40000)}\n…`:body});
    }catch(reason){setAnswer({status:0,type:'',body:(reason as Error).message})}
    finally{setBusy(false)}
  }
  return <div className="tryit">
    <button type="button" className="btn small" disabled={busy||missing.length>0} onClick={()=>void send()}>{busy?<Busy label="Sending…"/>:<><Icon name="play_arrow"/>Send</>}</button>
    {missing.length>0&&<span className="hint">Fill in {missing.join(' and ')} first.</span>}
    {answer&&<div className={`answer${answer.status>=400||answer.status===0?' bad':''}`}>
      <p className="answerhead"><b>{answer.status||'No answer'}</b>{answer.status>0&&<span>{answer.type.split(';')[0]}</span>}</p>
      <pre><code>{answer.body}</code></pre>
    </div>}
  </div>;
}

function RouteView({reference,route,addonName,addonOn}:{reference:Reference;route:Route;addonName?:string;addonOn?:boolean}){
  const {op}=route;
  const params=op.parameters??[];
  const [values,setValues]=useState<Record<string,string>>(()=>Object.fromEntries(params.map(param=>[param.name,paramValue(param)])));
  const [open,setOpen]=useState(false);
  const body=op.requestBody?.content['application/json']?.schema;
  const permission=op['x-cull-permission']??'read';
  const statuses=Object.keys(op.responses).sort();
  const readable=route.method==='get'&&!streams(route);
  return <details id={`op-${op.operationId}`} className="route" onToggle={event=>setOpen(event.currentTarget.open)}>
    <summary>
      <span className={`method ${route.method}`}>{route.method.toUpperCase()}</span>
      <code className="rpath">{route.path}</code>
      <span className="rsum">{op.summary}</span>
      <span className="rchips">
        {permission!=='read'&&<span className={`rchip perm ${permission}`} title={`Needs the ${permission} permission`}>{permission}</span>}
        {addonName&&<span className={`rchip addon${addonOn===false?' off':''}`} title={addonOn===false?`${addonName} is turned off, so this answers 404`:`Part of ${addonName}`}>{addonName}{addonOn===false?' · off':''}</span>}
        {op['x-cull-internal']&&<span className="rchip internal" title="For one of Cull's own helpers">internal</span>}
      </span>
    </summary>
    {open&&<div className="rbody">
      {op.description&&<p className="rdoc">{prose(op.description)}</p>}
      {params.length>0&&<><h4>Parameters</h4>
        <table className="params"><tbody>{params.map(param=><tr key={`${param.in}:${param.name}`}>
          <th scope="row"><code>{param.name}</code><span className="pin">{param.in}{param.required?', required':''}</span></th>
          <td><span className="ftype">{typeName(param.schema)}</span> {prose(param.description)}
            {readable&&<input className="pvalue" aria-label={param.name} value={values[param.name]??''} placeholder={param.required?'required':'optional'} onChange={event=>setValues(current=>({...current,[param.name]:event.target.value}))}/>}
          </td>
        </tr>)}</tbody></table></>}
      {body&&<><h4>Body</h4><SchemaView reference={reference} schema={body}/></>}
      <h4>Answers</h4>
      <ul className="answers">{statuses.map(status=>{
        const response=op.responses[status],content=response.content??{},[type]=Object.keys(content);
        return <li key={status}><b className={Number(status)>=400?'bad':undefined}>{status}</b> {prose(response.description)}{type&&!type.includes('json')&&<span className="ftype">{type}</span>}
          {type?.includes('json')&&content[type].schema&&<SchemaView reference={reference} schema={content[type].schema}/>}
        </li>;
      })}</ul>
      <h4>From an addon</h4>
      <Code text={curl(reference,route,values)}/>
      {readable&&<><h4>Try it</h4><TryIt route={route} values={values}/></>}
    </div>}
  </details>;
}

const manifestExample=JSON.stringify({
  id:'hello-cull',name:'Hello, Cull',version:'0.1.0',summary:'Shows how many files the library holds.',
  icon:'extension',
  pages:[{id:'hello',label:'Hello',icon:'extension',section:'tools',url:'http://127.0.0.1:8900/'}],
},null,2);

export function Developers(){
  const [reference,setReference]=useState<Reference|null>(null);
  const [error,setError]=useState('');
  const [query,setQuery]=useState('');
  const wanted=useDeferredValue(query.trim().toLowerCase());
  const addons=useAddons();
  useEffect(()=>{readReference().then(setReference).catch(reason=>setError((reason as Error).message))},[]);
  const groups=useMemo(()=>{
    if(!reference)return [];
    const routes:Route[]=Object.entries(reference.paths).flatMap(([path,ops])=>Object.entries(ops).map(([method,op])=>({method,path,op})));
    routes.sort((a,b)=>a.path.localeCompare(b.path)||methodOrder.indexOf(a.method)-methodOrder.indexOf(b.method));
    return reference.tags.map(tag=>({tag,routes:routes.filter(route=>route.op.tags?.[0]===tag.name)})).filter(group=>group.routes.length>0);
  },[reference]);
  const shown=useMemo(()=>groups.map(group=>({...group,routes:wanted?group.routes.filter(({method,path,op})=>`${method} ${path} ${op.summary} ${op.description??''}`.toLowerCase().includes(wanted)):group.routes})).filter(group=>group.routes.length>0),[groups,wanted]);
  // The page is drawn once the reference arrives, so a link to a route or a
  // section is followed then, and again whenever a link on the page is
  // followed: the route opens and comes into view.
  useEffect(()=>{
    if(!reference)return;
    const follow=()=>{
      if(!location.hash)return;
      const target=document.getElementById(decodeURIComponent(location.hash.slice(1)));
      if(target instanceof HTMLDetailsElement)target.open=true;
      target?.scrollIntoView();
    };
    follow();
    window.addEventListener('hashchange',follow);
    return()=>window.removeEventListener('hashchange',follow);
  },[reference]);
  const count=groups.reduce((sum,group)=>sum+group.routes.length,0);
  return <section className="developers">
    <header className="addonshead">
      <h1>Developers</h1>
      <p className="ysum">Cull’s API is the one its own pages use. An addon can do anything a page can, within the permissions it asks for, in whatever language it is written.{reference&&<> Version {reference['x-cull-api']??reference.info.version}, {count.toLocaleString()} routes.</>}</p>
      <p className="addonslinks"><a href="#start">Start here</a><a href="#build">Build an addon</a><a href="#permissions">Permissions</a><a href="/api/openapi.json" target="_blank" rel="noreferrer">openapi.json</a></p>
    </header>
    {error&&<p className="note warn" role="alert">{error}</p>}
    {!reference&&!error&&<Busy size={64} label="Reading the API reference…"/>}
    {reference&&<div className="devgrid">
      <nav className="devnav" aria-label="API sections">
        <a href="#start">Start here</a><a href="#build">Build an addon</a><a href="#permissions">Permissions</a><a href="/api/openapi.json" target="_blank" rel="noreferrer">openapi.json</a>
        <h2>Reference</h2>
        {groups.map(group=><a key={group.tag.name} href={`#${slug(group.tag.name)}`}>{group.tag.name}<span className="n">{group.routes.length}</span></a>)}
      </nav>
      <div className="devmain">
        <section id="start" className="devsec">
          <h2>Start here</h2>
          <p>Every route is under <code>{location.origin}/api</code> and speaks JSON. Cull has no accounts: its own pages send no key, and anything that can reach Cull can use it, so keep it on your own network. An addon sends its key, which says which addon is asking, so turning the addon off stops it and it can do no more than it asked for.</p>
          <p>A failure is a status of 400 or more with a sentence to show: <code>{'{"error": "…"}'}</code>. Choices are records only: files move when they are sent to the Bin, and nothing leaves the Bin without being asked for twice.</p>
          <p>To follow what happens without asking again and again, read <a href="#op-getEvents"><code>GET /api/events</code></a>, a stream of server-sent events.</p>
        </section>
        <section id="build" className="devsec">
          <h2>Build an addon</h2>
          <ol className="steps">
            <li><p>Make a folder in Cull’s addons folder, named as the addon’s <code>id</code>, holding an <code>addon.json</code>:</p><Code text={manifestExample}/></li>
            <li><p>Open <a href="/addons">Addons</a>. It shows there a moment later. Turn it on and Cull writes a <code>key</code> file beside the manifest, readable only by its owner.</p></li>
            <li><p>Send the key with every request:</p><Code text={`curl -H "Authorization: Bearer $(cat key)" ${location.origin}/api/stats`}/></li>
            <li><p>A page in <code>pages</code> shows in Cull’s sidebar, and Cull shows the <code>url</code> it names inside its frame. The frame is told Cull’s theme with a message of type <code>cull:hello</code>, again as <code>cull:theme</code> when it changes, and can send <code>{'{"type": "cull:navigate", "path": "/bin"}'}</code> to move Cull to one of its own pages.</p></li>
          </ol>
          <p className="hint">Icons are Material Symbols names from the set Cull ships; any other is drawn as a puzzle piece. The repository’s <code>docs/ADDONS.md</code> says all of it at more length, and <code>examples/addons</code> holds addons to start from.</p>
        </section>
        <section id="permissions" className="devsec">
          <h2>Permissions</h2>
          <p>Every addon may read. Anything more is asked for in <code>permissions</code>, shown before the addon is turned on, and checked on every request.</p>
          <dl className="kv">{(reference['x-cull-permissions']??[]).map(permission=><div key={permission.name}><dt><code>{permission.name}</code></dt><dd>{permission.description}</dd></div>)}</dl>
        </section>
        <div className="devfilter">
          <Icon name="search"/>
          <input type="search" value={query} onChange={event=>setQuery(event.target.value)} placeholder="Find a route, such as bin or /api/tasks" aria-label="Find a route"/>
        </div>
        {shown.length===0&&<p className="note">No route matches “{query.trim()}”.</p>}
        {shown.map(group=><section key={group.tag.name} id={slug(group.tag.name)} className="devsec reftag">
          <h2>{group.tag.name}</h2>
          {group.tag.description&&<p className="tagdoc">{prose(group.tag.description)}</p>}
          <div className="routes">{group.routes.map(route=>{
            const owner=route.op['x-cull-addon'],addon=owner?addons?.find(item=>item.id===owner):undefined;
            return <RouteView key={`${route.method} ${route.path}`} reference={reference} route={route} addonName={owner?addon?.name??owner:undefined} addonOn={addon?.on}/>;
          })}</div>
        </section>)}
      </div>
    </div>}
  </section>;
}
