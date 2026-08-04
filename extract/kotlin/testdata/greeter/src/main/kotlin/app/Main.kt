package com.example.app

import com.example.greeter.Greeter

fun main() {
    val g = Greeter("world")
    val message = g.greet()
    println(message)
}
