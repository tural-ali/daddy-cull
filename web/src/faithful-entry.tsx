import {createRoot} from 'react-dom/client';
import {App} from './faithful/App';
import './faithful/legacy.css';

createRoot(document.getElementById('root')!).render(<App/>);
