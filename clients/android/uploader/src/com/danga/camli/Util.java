package com.danga.camli;

import java.io.BufferedInputStream;
import java.io.FileDescriptor;
import java.io.FileInputStream;
import java.io.IOException;
import java.io.InputStream;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;

public class Util {
    public static String slurp(InputStream in) throws IOException {
        StringBuilder sb = new StringBuilder();
        byte[] b = new byte[4096];
        for (int n; (n = in.read(b)) != -1;) {
            sb.append(new String(b, 0, n));
        }
        return sb.toString();
    }

    private static String convertToHex(byte[] data) {
        StringBuilder buf = new StringBuilder();
        for (int i = 0; i < data.length; i++) {
            int halfbyte = (data[i] >>> 4) & 0x0F;
            int two_halfs = 0;
            do {
                if ((0 <= halfbyte) && (halfbyte <= 9))
                    buf.append((char) ('0' + halfbyte));
                else
                    buf.append((char) ('a' + (halfbyte - 10)));
                halfbyte = data[i] & 0x0F;
            } while (two_halfs++ < 1);
        }
        return buf.toString();
    }

    private static final String HEX = "0123456789abcdef";

    private static String getHex(byte[] raw) {
        if (raw == null) {
            return null;
        }
        final StringBuilder hex = new StringBuilder(2 * raw.length);
        for (final byte b : raw) {
            hex.append(HEX.charAt((b & 0xF0) >> 4)).append(
                    HEX.charAt((b & 0x0F)));
        }
        return hex.toString();
    }

    public static String getSha1(FileDescriptor fd) {
        MessageDigest md;
        try {
            md = MessageDigest.getInstance("SHA-1");
        } catch (NoSuchAlgorithmException e) {
            throw new RuntimeException(e);
        }
        byte[] b = new byte[4096];
        FileInputStream fis = new FileInputStream(fd);
        InputStream is = new BufferedInputStream(fis, 4096);
        try {
            for (int n; (n = is.read(b)) != -1;) {
                md.update(b, 0, n);
            }
        } catch (IOException e) {
            throw new RuntimeException(e);
        }
        byte[] sha1hash = new byte[40];
        sha1hash = md.digest();
        return getHex(sha1hash);
    }
}
