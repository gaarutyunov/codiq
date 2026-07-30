package main

import "fmt"

func main() {
	g := &Greeter{Name: "world"}
	fmt.Println(g.Greet())
}
