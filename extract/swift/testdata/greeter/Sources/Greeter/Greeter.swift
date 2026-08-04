/// Greeter greets by name.
public struct Greeter: Speaker {
    public var name: String

    /// greet returns this Greeter's greeting.
    public func greet() -> String {
        return "hello, " + name
    }
}
