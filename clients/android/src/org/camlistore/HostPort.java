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
            mValid = hostPort.length() > 0;
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
