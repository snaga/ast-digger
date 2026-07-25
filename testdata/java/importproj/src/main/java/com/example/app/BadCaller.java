package com.example.app;

import com.example.doesnotexist.Something;

public class BadCaller {
    public static void run() {
        Something.doIt();
    }
}
