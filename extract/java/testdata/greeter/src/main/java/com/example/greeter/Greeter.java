package com.example.greeter;

/** Greeter greets by name. */
public class Greeter implements Speaker {
    private final String name;

    /** Greeter builds a Greeter for name. */
    public Greeter(String name) {
        this.name = name;
    }

    /** greet returns this Greeter's greeting. */
    public String greet() {
        return "hello, " + this.name;
    }
}
