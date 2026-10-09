import {Icon} from '../Icon';

/** Cull on an iPad is laid out for the iPad held sideways. Opened from the
 * Home Screen and turned upright, it asks to be turned back rather than
 * squeezing the review into a column; see .landscape in the stylesheet for
 * when it shows. iPadOS ignores the manifest's orientation, so the app says it
 * itself. */
export function Landscape(){
  return <div className="landscape">
    <Icon name="screen_rotation_alt"/>
    <b>Turn your iPad sideways</b>
    <span>Cull is laid out for landscape.</span>
  </div>;
}
