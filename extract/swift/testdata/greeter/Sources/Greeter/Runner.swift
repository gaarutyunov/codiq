/// run builds a Greeter and asks it for its greeting.
public func run() -> String {
    let g = Greeter(name: "world")
    let message = g.greet()
    return message
}
