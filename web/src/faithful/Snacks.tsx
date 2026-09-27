import type {ReactNode} from 'react';
import {createPortal} from 'react-dom';

/** Every notice stacks in the frame's one corner, so a page's own snackbars
 * and the frame's never land on top of each other. The frame's container is
 * on the page before any page content renders; without it, as in a page shown
 * on its own, the notices sit in a corner of their own. */
export function Snacks({children}:{children:ReactNode}){
  const root=document.getElementById('snacks');
  return root?createPortal(children,root):<div className="snacks">{children}</div>;
}
