/*
Copyright 2011 Google Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
 */

package org.camlistore;

/**
 * HostPort parses a "host.com", "host.com:port", or "https://host.com:port"
 * It doesn't handle paths.  TODO(bradfitz): This should probably be scrapped
 * and use a URL parser or something instead.
 */
public class HostPort {
    private final boolean mValid;
    private final String mHost;
    private final int mPort;
    private final boolean mSecure;

    private static final String HTTP_PREFIX = "http://";
    private static final String SECURE_PREFIX = "https://";

    public HostPort(String hostPort) {
        if (hostPort.startsWith(HTTP_PREFIX)) {
            mSecure = false;
            hostPort = hostPort.substring(HTTP_PREFIX.length());
        } else if (hostPort.startsWith(SECURE_PREFIX)) {
            mSecure = true;
            hostPort = hostPort.substring(SECURE_PREFIX.length());
        } else {
            mSecure = false;
        }

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
            mValid = hostPort.length() > 0;
            mHost = hostPort;
            mPort = mSecure ? 443 : 80;
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

    public boolean isSecure() {
        return mSecure;
    }

    private boolean nonStandardPort() {
        return mPort != (mSecure ? 443 : 80);
    }

    public String urlPrefix() {
        StringBuilder sb = new StringBuilder(12 + mHost.length());
        sb.append(httpScheme());
        sb.append("://");
        sb.append(mHost);
        if (nonStandardPort()) {
            sb.append(":");
            sb.append(mPort);
        }
        return sb.toString();
    }

    public String httpScheme() {
        return mSecure ? "https" : "http";
    }

    @Override
    public String toString() {
        if (!mValid) {
            return "[invalid HostPort]";
        }
        return mHost + ":" + mPort;
    }
}
