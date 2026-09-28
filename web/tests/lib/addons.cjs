// Synthetic addons and a small API reference for the addon pages' tests.
// Nothing here names a real person, file or address beyond the test hosts.

const page=(id,label,icon,section,path,url)=>({id,label,icon,section,path,...(url?{url}:{})});

function builtIns(){
  return [
    {id:'screenshots',name:'Screenshots',version:'1',summary:'Review screenshots apart from the photos.',icon:'screenshot_region',pages:[page('collections','Screenshots','screenshot_region','collections','/screenshots')],
      work:['Sorts new files into screenshots and photos'],builtIn:true,on:true,chosen:true,status:{state:'ready',detail:'12 screenshots wait for review.'},routes:5},
    {id:'social',name:'Saved from social',version:'1',summary:'Find videos saved from social apps.',icon:'forum',pages:[page('social','Saved from social','forum','collections','/social')],
      builtIn:true,on:false,chosen:false,status:{state:'setup',detail:'No detection report has been imported yet.'},routes:2},
    {id:'apple-photos',name:'Apple Photos',version:'1',summary:'Carry what you remove across to Apple Photos.',icon:'cloud_sync',pages:[page('sync','Apple Photos','cloud_sync','sync','/photos')],
      needs:['A Mac signed in to iCloud'],builtIn:true,on:true,chosen:true,status:{state:'setup',detail:'Cull Sync is not set up yet.'},routes:6},
  ];
}

function yours(){
  return [
    {id:'hello-cull',name:'Hello, Cull',version:'0.1.0',summary:'Shows what the library holds.',description:'The smallest addon with a page.',author:'Test Author',homepage:'https://example.test/hello',icon:'extension',
      pages:[page('hello','Hello','waving_hand','tools','/addons/hello-cull/hello','http://localhost:8842/hello-frame/')],permissions:['review','delete'],
      builtIn:false,on:false,chosen:false,status:{state:'ready',detail:'Its key is written to its folder when it is turned on.'},routes:0,folder:'/state/addons/hello-cull'},
    {id:'broken',name:'Broken',version:'0.0.1',summary:'A manifest with a mistake.',builtIn:false,on:false,chosen:false,status:{state:'problem',detail:'addon.json could not be read.'},routes:0,folder:'/state/addons/broken',problem:'addon.json: unexpected end of JSON input.'},
  ];
}

const reference={
  openapi:'3.1.0',
  info:{title:'Daddy, Cull!',version:'1.0.0'},
  'x-cull-api':'1',
  'x-cull-permissions':[
    {name:'read',description:'Read the catalogue, choices and settings.'},
    {name:'review',description:'Save choices and hearts.'},
    {name:'bin',description:'Move files to the Bin and back.'},
    {name:'delete',description:'Delete files in the Bin for good.'},
    {name:'settings',description:'Change settings and turn addons on or off.'},
  ],
  tags:[{name:'Library',description:'What the catalogue holds.'},{name:'Screenshots',description:'Screenshots, apart from the photos.'},{name:'Events',description:'What happens, as it happens.'}],
  paths:{
    '/api/stats':{get:{operationId:'getStats',summary:'Counts for the whole library',tags:['Library'],
      parameters:[{name:'tz',in:'query',description:'An IANA time zone for today.',schema:{type:'string'},example:'Europe/London'}],
      responses:{200:{description:'The counts.',content:{'application/json':{schema:{$ref:'#/components/schemas/Stats'}}}}}}},
    '/api/assets/{id}':{get:{operationId:'getAsset',summary:'One file',tags:['Library'],
      parameters:[{name:'id',in:'path',required:true,description:'The file’s id.',schema:{type:'integer'}}],
      responses:{200:{description:'The file.',content:{'application/json':{schema:{type:'object',properties:{id:{type:'integer',description:'Its id.'}}}}}},404:{description:'No such file.'}}}},
    '/api/screenshots/remove':{post:{operationId:'postScreenshotsRemove',summary:'Move screenshots to the Bin',tags:['Screenshots'],'x-cull-permission':'bin','x-cull-addon':'social',
      requestBody:{required:true,content:{'application/json':{schema:{type:'object',required:['ids'],properties:{ids:{type:'array',items:{type:'integer'},description:'The files.'}}}}}},
      responses:{202:{description:'Queued as a task.'}}}},
    '/api/events':{get:{operationId:'getEvents',summary:'Follow what happens',tags:['Events'],
      responses:{200:{description:'A stream of server-sent events.',content:{'text/event-stream':{schema:{type:'string'}}}}}}},
  },
  components:{schemas:{Stats:{type:'object','x-order':['total','bin'],required:['total','bin'],properties:{total:{type:'integer',description:'Files in the catalogue.'},bin:{type:'integer',description:'Files in the Bin.'}}}}},
};

/** Answers the routes every page asks for, so each test mocks only its own. */
function common(url){
  if(url.pathname==='/api/stats')return {json:{total:10,synthetic:false,snapshotAt:'',screenshots:12,bin:0,notifications:0}};
  if(url.pathname==='/api/notifications')return {json:{unread:0,items:[]}};
  if(url.pathname==='/api/tasks')return {json:{tasks:[],active:0}};
  if(url.pathname==='/api/openapi.json')return {json:reference};
  return null;
}

module.exports={builtIns,yours,reference,common};
