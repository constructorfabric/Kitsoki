// cytoscape-cola/fcose/klay ship no type declarations of their own; each is
// a `cytoscape.use(plugin)` extension registered once in layouts.ts.
declare module "cytoscape-cola" {
  const ext: (cy: unknown) => void;
  export default ext;
}
declare module "cytoscape-fcose" {
  const ext: (cy: unknown) => void;
  export default ext;
}
declare module "cytoscape-klay" {
  const ext: (cy: unknown) => void;
  export default ext;
}
