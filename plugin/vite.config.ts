import { defineConfig } from 'vite';
import { ervisioPlugin } from '@ervisio/plugin-sdk/vite';

// One self-contained ES module at dist/repo/index.js. React is never bundled: `react` and the JSX runtime are
// aliased by the preset to the SDK's shims, which forward to sdk.react (see @ervisio/plugin-sdk/react).
export default defineConfig(ervisioPlugin({ id: 'repo', entry: 'src/index.ts' }));
