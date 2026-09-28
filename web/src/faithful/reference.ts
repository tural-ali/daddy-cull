// The API reference, as the server describes itself at /api/openapi.json.
// It is read once a page load, by the Developers page and by the Addons page
// for what each permission lets an addon do.

export type Schema={
  type?:string|string[];$ref?:string;description?:string;format?:string;enum?:unknown[];
  items?:Schema;properties?:Record<string,Schema>;required?:string[];additionalProperties?:Schema|boolean;
  anyOf?:Schema[];oneOf?:Schema[];allOf?:Schema[];nullable?:boolean;example?:unknown;'x-order'?:string[];
};
export type Parameter={name:string;in:'path'|'query'|'header';description?:string;required?:boolean;schema?:Schema;example?:unknown};
export type Operation={
  operationId:string;summary:string;description?:string;tags?:string[];parameters?:Parameter[];
  requestBody?:{required?:boolean;content:Record<string,{schema:Schema}>};
  responses:Record<string,{description:string;content?:Record<string,{schema:Schema}>}>;
  'x-cull-permission'?:string;'x-cull-addon'?:string;'x-cull-internal'?:boolean;
};
export type Permission={name:string;description:string};
export type Reference={
  info:{title:string;version:string;description?:string};
  tags:{name:string;description?:string}[];
  paths:Record<string,Record<string,Operation>>;
  components:{schemas:Record<string,Schema>};
  'x-cull-api'?:string;'x-cull-permissions'?:Permission[];
};

let reading:Promise<Reference>|null=null;

/** The reference, read once and shared. A read that fails is tried again
 * the next time it is asked for. */
export function readReference():Promise<Reference>{
  reading??=fetch('/api/openapi.json').then(async response=>{
    if(!response.ok)throw new Error('The API reference could not be read. Check that Cull is running, then try again.');
    return await response.json() as Reference;
  }).catch(reason=>{reading=null;throw reason});
  return reading;
}
