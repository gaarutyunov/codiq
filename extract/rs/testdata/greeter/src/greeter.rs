//! Greeter greets by name.

/// Greeter greets by name.
pub struct Greeter {
    pub name: String,
}

impl Greeter {
    /// new builds a Greeter for name.
    pub fn new(name: &str) -> Greeter {
        Greeter { name: name.to_string() }
    }

    /// greet returns this Greeter's greeting.
    pub fn greet(&self) -> String {
        let mut out = String::from("hello, ");
        out.push_str(&self.name);
        out
    }
}
