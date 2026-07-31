package com.example.app;

import com.example.greeter.Greeter;

public final class Main {
    public static void main(String[] args) {
        Greeter g = new Greeter("world");
        String message = g.greet();
        System.out.println(message);
    }
}
