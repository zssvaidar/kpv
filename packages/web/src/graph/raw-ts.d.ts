// A module imported with ?raw is its source text, which is how edit.test.ts
// checks what the editing modules import. Vite and Vitest both resolve the
// suffix.
declare module '*.ts?raw' {
  const text: string;
  export default text;
}
