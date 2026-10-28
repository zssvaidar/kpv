// A stylesheet imported with ?raw is its text, which is how blocks.test.ts
// reads the colours it checks. Vite and Vitest both resolve the suffix.
declare module '*.css?raw' {
  const text: string;
  export default text;
}
