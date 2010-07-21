package com.danga.camli;

public class HostPort {
    private final boolean mValid;
    private final String mHost;
    private final int mPort;

    public HostPort(String hostPort) {
        String[] parts = hostPort.split(":");
        if (parts.length == 2) {
            mHost = parts[0];
            mPort = new Integer(parts[1]).intValue();
            mValid = true;
        } else if (parts.length > 2 || parts.length == 0) {
            mValid = false;
            mHost = null;
            mPort = 0;
        } else {
            mValid = true;
            mHost = hostPort;
            mPort = 80;
        }
    }

    public int port() {
        return mPort;
    }

    public String host() {
        return mHost;
    }

    public boolean isValid() {
        return mValid;
    }

    @Override
    public String toString() {
        if (!mValid) {
            return "[invalid HostPort]";
        }
        return mHost + ":" + mPort;
    }
}
