// Greeter greets by name.
export class Greeter {
  name: string;

  constructor(name: string) {
    this.name = name;
  }

  // greet returns this Greeter's greeting.
  greet(): string {
    return "hello, " + this.name;
  }
}
