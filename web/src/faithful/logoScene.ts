import {AmbientLight,DirectionalLight,ExtrudeGeometry,type BufferGeometry,Mesh,MeshBasicMaterial,MeshLambertMaterial,PerspectiveCamera,Scene,WebGLRenderer} from 'three';
import {toCreasedNormals} from 'three/addons/utils/BufferGeometryUtils.js';
import {SVGLoader} from 'three/addons/loaders/SVGLoader.js';

/** A single rigid extrusion of the actual brand paths, including letter holes.
 * Only the camera moves, along Z through the centre of the full SVG viewbox.
 * Loaded on demand, and disposed when the opening ends. */
export function logoScene(host:HTMLElement,svg:SVGSVGElement){
  const source=svg.cloneNode(true) as SVGSVGElement;
  const originals=[...svg.querySelectorAll('path')];
  source.querySelectorAll('path').forEach((path,index)=>path.setAttribute('fill',getComputedStyle(originals[index]).fill));
  const renderer=new WebGLRenderer({alpha:true,antialias:true});
  // MSAA keeps the letter edges clean without a full retina-sized framebuffer.
  renderer.setPixelRatio(Math.min(devicePixelRatio,1.5));
  renderer.domElement.className='openingcanvas';
  renderer.domElement.setAttribute('aria-hidden','true');
  const scene=new Scene();
  const camera=new PerspectiveCamera(42,1,.1,30000);
  const ambient=new AmbientLight(0xffffff,1.2);
  const key=new DirectionalLight(0xffffff,2);
  key.position.set(-800,900,1400);
  scene.add(ambient,key);
  const meshes:Mesh<BufferGeometry,(MeshBasicMaterial|MeshLambertMaterial)[]>[]=[];
  for(const path of new SVGLoader().parse(source.outerHTML).paths){
    const shapes=SVGLoader.createShapes(path);
    if(shapes.length===0)continue;
    const extrusion=new ExtrudeGeometry(shapes,{depth:96,bevelEnabled:false,curveSegments:8,steps:1});
    const geometry=toCreasedNormals(extrusion,.7);
    if(geometry!==extrusion)extrusion.dispose();
    geometry.translate(-1072,-360,-96);
    // Matte faces retain the brand colours; fixed light shades the walls.
    const materials=[new MeshBasicMaterial({color:path.color}),new MeshLambertMaterial({color:path.color.clone().multiplyScalar(.48)})];
    const mesh=new Mesh(geometry,materials);
    mesh.scale.y=-1;
    scene.add(mesh);meshes.push(mesh);
  }
  let start=0,progress=0;
  const draw=()=>{
    const t=progress*progress*(3-2*progress);
    camera.position.set(0,0,start*(1-t)**3-180*t**3);
    // Keep orientation fixed even after crossing the logo's front plane.
    renderer.render(scene,camera);
    renderer.domElement.dataset.cameraZ=camera.position.z.toFixed(2);
  };
  renderer.domElement.dataset.parts=String(meshes.length);
  const resize=()=>{
    const width=host.clientWidth,height=host.clientHeight;
    renderer.setSize(width,height);
    camera.aspect=width/height;camera.updateProjectionMatrix();
    const scale=Math.min(width*.76,620)/2016;
    start=height/(2*Math.tan(camera.fov*Math.PI/360)*scale);
    draw();
  };
  resize();host.append(renderer.domElement);
  const observer=new ResizeObserver(resize);observer.observe(host);
  return {
    draw(at:number){
      progress=Math.min(1,Math.max(0,at));
      draw();
    },
    dispose(){observer.disconnect();for(const mesh of meshes){mesh.geometry.dispose();for(const material of mesh.material)material.dispose()}renderer.dispose();renderer.forceContextLoss();renderer.domElement.remove()},
  };
}
