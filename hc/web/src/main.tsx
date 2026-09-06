import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from './App';
import './styles.css';

const root = document.querySelector('#root');
if (root === null) {
  throw new Error('HC root element was not found');
}

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
