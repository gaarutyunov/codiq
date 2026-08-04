package com.example.greeter

/** Greeter greets by name. */
class Greeter(private val name: String) : Speaker {
    /** greet returns this Greeter's greeting. */
    override fun greet(): String = "hello, $name"
}
