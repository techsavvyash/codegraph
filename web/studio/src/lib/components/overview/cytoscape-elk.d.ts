/**
 * cytoscape-elk ships no type declarations of its own. It's a standard
 * cytoscape extension: the default export is the function passed to
 * cytoscape.use(ext) to register the 'elk' layout (elkjs bundled).
 */
declare module 'cytoscape-elk' {
  import type cytoscape from 'cytoscape'
  const elk: cytoscape.Ext
  export default elk
}
