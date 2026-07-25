package com.example.sample;

import java.util.List;

/**
 * SampleClass demonstrates fields, a constructor, plain methods, and an
 * overloaded method pair for JavaParser test coverage.
 */
public class SampleClass {
    private String name;
    private int value;

    /**
     * Constructs a SampleClass with the given name.
     *
     * @param name the name to use
     */
    public SampleClass(String name) {
        this.name = name;
        this.value = 0;
    }

    /**
     * Returns a greeting for this instance.
     */
    public String greet() {
        return "Hello, " + name;
    }

    /**
     * Adds two integers.
     */
    public int add(int a, int b) {
        return a + b;
    }

    // Overload of add(int, int); this is a plain comment, not Javadoc, so
    // this method must have no Docstring.
    public int add(int a, int b, int c) {
        return a + b + c;
    }

    public void noDoc() {
    }
}

/**
 * Greetable describes something that can produce a greeting.
 */
interface Greetable {
    /**
     * Produces a greeting string.
     */
    String greet();
}

/**
 * Color enumerates a small set of colors.
 */
enum Color {
    RED,
    GREEN,
    BLUE;

    /**
     * Returns whether this color is RED.
     */
    public boolean isRed() {
        return this == RED;
    }
}
