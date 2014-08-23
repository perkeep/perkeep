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

import android.content.SharedPreferences;

public final class Preferences {
    public static final String NAME = "CamliUploader";

    public static final String HOST = "camli.host";
    // TODO(mpl): list instead of single string later? seems overkill for now.
    public static final String TRUSTED_CERT = "camli.trusted_cert";
    public static final String USERNAME = "camli.username";
    public static final String PASSWORD = "camli.password";
    public static final String AUTO = "camli.auto";
    public static final String AUTO_OPTS = "camli.auto.opts";
    public static final String MAX_CACHE_MB = "camli.max_cache_mb";
    public static final String DEV_IP = "camli.dev_ip";
    public static final String AUTO_REQUIRE_POWER = "camli.auto.require_power";
    public static final String AUTO_REQUIRE_WIFI = "camli.auto.require_wifi";
    public static final String AUTO_REQUIRED_WIFI_SSID = "camli.auto.required_wifi_ssid";
    public static final String AUTO_DIR_PHOTOS = "camli.auto.photos";
    public static final String AUTO_DIR_MYTRACKS = "camli.auto.mytracks";

    private final SharedPreferences mSP;

    public Preferences(SharedPreferences prefs) {
        mSP = prefs;
    }

    public boolean autoRequiresPower() {
        return mSP.getBoolean(AUTO_REQUIRE_POWER, false);
    }

    public boolean autoRequiresWifi() {
        return mSP.getBoolean(AUTO_REQUIRE_WIFI, false);
    }

    public String autoRequiredWifiSSID() {
        return mSP.getString(AUTO_REQUIRED_WIFI_SSID, "");
    }

    public boolean autoUpload() {
        return mSP.getBoolean(AUTO, false);
    }

    public int maxCacheMb() {
        return Integer.parseInt(mSP.getString(MAX_CACHE_MB, "256"));
    }

    public long maxCacheBytes() {
        return maxCacheMb() * 1024 * 1024;
    }

    public boolean autoDirPhotos() {
        return mSP.getBoolean(AUTO_DIR_PHOTOS, true);
    }

    public boolean autoDirMyTracks() {
        return mSP.getBoolean(AUTO_DIR_MYTRACKS, true);
    }

    private String devIP() {
        return mSP.getString(DEV_IP, "");
    }

    private boolean inDevMode() {
        return !devIP().isEmpty();
    }

    public String username() {
        if (inDevMode()) {
            return "camlistore";
        }
        return mSP.getString(USERNAME, "");
    }

    public String password() {
        if (inDevMode()) {
            return "pass3179";
        }
        return mSP.getString(PASSWORD, "");
    }

    public HostPort hostPort() {
        if (inDevMode()) {
            return new HostPort("http://" + devIP() + ":3179");
        }
        return new HostPort(mSP.getString(Preferences.HOST, ""));
    }

    public String trustedCert() {
        return mSP.getString(TRUSTED_CERT, "").toLowerCase();
    }

    public void setDevIP(String value) {
        mSP.edit().putString(DEV_IP, value).apply();
    }

}
