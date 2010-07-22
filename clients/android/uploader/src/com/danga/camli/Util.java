package com.danga.camli;

import java.io.IOException;
import java.io.InputStream;

public class Util {
    public static String slurp(InputStream in) throws IOException {
        StringBuilder sb = new StringBuilder();
        byte[] b = new byte[4096];
        for (int n; (n = in.read(b)) != -1;) {
            sb.append(new String(b, 0, n));
        }
        return sb.toString();
    }
}
