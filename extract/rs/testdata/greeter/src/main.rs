mod greeter;

use crate::greeter::Greeter;

fn main() {
    let g = Greeter { name: String::from("world") };
    let message = g.greet();
    println!("{}", message);
}
