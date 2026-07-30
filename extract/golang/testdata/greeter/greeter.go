package main

// Greeter greets by name.
type Greeter struct {
	Name string
}

// Greet returns this Greeter's greeting.
func (g *Greeter) Greet() string {
	return "hello, " + g.Name
}
