import { Greeter } from "./greeter";

export function main(): void {
  const g = new Greeter("world");
  console.log(g.greet());
}
