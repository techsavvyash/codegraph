/**
 * cytoscape-fcose ships no type declarations of its own. It's a standard
 * cytoscape extension: the default export is the function passed to
 * cytoscape.use(ext) to register the 'fcose' layout.
 */
declare module 'cytoscape-fcose' {
  import type cytoscape from 'cytoscape'
  const fcose: cytoscape.Ext
  export default fcose
}
