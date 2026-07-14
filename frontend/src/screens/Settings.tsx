// Settings moved into ./settings/ (split by section: Connections / Auth / AI /
// Library / Advanced, plus shared panel primitives). This thin re-export keeps
// the public import path stable — `import { Settings } from "./Settings"` (and
// "./screens/Settings" from outside) still resolves — so nothing else in the
// codebase or the test suite has to change its imports.
export { Settings } from "./settings";
