// The server's task queue for mocked pages. A task does its work the moment
// it is queued, through the test's own `apply`, and answers as queued; the
// next read of the list finds it finished, as if the runner had got to it.
// `undo` takes back what a move of screenshots did.
module.exports=function taskQueue({apply,undo}){
  const tasks=[],posts=[];
  let next=0;
  const now=()=>new Date().toISOString();
  const view=task=>{const copy={...task};delete copy.outcome;return copy};
  function queue(kind,label,body){
    const outcome=kind==='screenshots.restore'?undo(body.source):apply(kind,body);
    const task={id:`task-${++next}`,kind,label,state:'queued',total:outcome.total,done:0,failed:0,cancelled:0,bytes:0,failures:[],
      createdAt:now(),undoable:kind==='screenshots.remove',outcome};
    tasks.unshift(task);
    return task;
  }
  function settle(){
    for(const task of tasks){
      if(task.state!=='queued')continue;
      const {done=task.total,failures=[],bytes=0,keptDays}=task.outcome;
      Object.assign(task,{state:failures.length>0?'failed':'done',done,failed:failures.length,failures,bytes,finishedAt:now(),...(keptDays?{keptDays}:{})});
    }
  }
  const list=()=>({tasks:tasks.map(view),active:tasks.filter(task=>task.state==='queued'||task.state==='running').length});
  /** Answers a request under /api/tasks, or returns undefined for any other. */
  function handle(route,url,request){
    if(!url.pathname.startsWith('/api/tasks'))return undefined;
    if(url.pathname==='/api/tasks'){
      const answer=list();
      settle();
      return route.fulfill({json:answer});
    }
    const body=request.postDataJSON()||{};
    posts.push({path:url.pathname,body});
    if(url.pathname==='/api/tasks/screenshots')return route.fulfill({status:202,json:view(queue(`screenshots.${body.action}`,`Move ${body.assetIds.length} screenshots`,body))});
    if(url.pathname==='/api/tasks/bin')return route.fulfill({status:202,json:view(queue(body.action==='restore'?'bin.restore':body.action==='purge-now'?'bin.purge-now':'bin.delete',`${body.action} from the Bin`,body))});
    if(url.pathname==='/api/tasks/clear'){tasks.splice(0,tasks.length,...tasks.filter(task=>task.state==='queued'||task.state==='running'));return route.fulfill({json:list()})}
    const [,,,id,action]=url.pathname.split('/');
    const task=tasks.find(item=>item.id===id);
    if(!task)return route.fulfill({status:404,json:{error:'There is no such task.'}});
    if(action==='undo'){
      if(!task.undoable)return route.fulfill({status:409,json:{error:'This task cannot be taken back.'}});
      task.undoable=false;
      return route.fulfill({status:202,json:view(queue('screenshots.restore','Bring back screenshots',{source:task.outcome}))});
    }
    return route.fulfill({status:404,json:{error:'not mocked'}});
  }
  return {handle,posts,tasks};
};
